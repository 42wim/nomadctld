package main

import (
	"bufio"
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math"
	"os/exec"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	humanize "github.com/dustin/go-humanize"
	"github.com/gliderlabs/ssh"
	"github.com/spf13/viper"
)

func hasPrefix(name string, prefixes []string) bool {
	for _, prefix := range prefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func contains(name string, array []string) bool {
	for _, e := range array {
		if e == name {
			return true
		}
	}
	return false
}

func validCmd(sess ssh.Session, cmds []string) bool {
	allowed := []string{"logs", "ps", "tail", "inspect", "exec", "attach", "stop", "raw", "rawl", "di", "tcpdump", "ipset"}
	needarg := []string{"logs", "tail", "inspect", "exec", "attach", "stop", "raw", "rawl", "di", "tcpdump", "ipset"}
	if len(cmds) == 0 {
		fmt.Fprintf(sess, "Only %v commands supported\n", allowed)
		return false
	}
	// need extra arg
	if contains(cmds[0], needarg) && len(cmds) == 1 {
		fmt.Fprintf(sess, "Extra arg needed for %s\n", cmds[0])
		return false
	}
	for alias, aliascmd := range viper.GetStringMapString("alias") {
		if cmds[0] == alias {
			newcmds := []string{}
			newcmds = append(newcmds, strings.Fields(aliascmd)...)
			newcmds = append(newcmds, cmds[1:]...)
			cmds = newcmds
		}
	}
	if !contains(cmds[0], allowed) {
		fmt.Fprintf(sess, "Only %v commands supported\n", allowed)
		return false
	}
	needtty := []string{"exec", "attach"}
	_, _, hasPty := sess.Pty()
	if contains(cmds[0], needtty) && !hasPty {
		fmt.Fprintf(sess, "You need a tty, run ssh -t\n")
		return false
	}
	return true
}

func isJobAllowed(n *NomadTier, jobID string, prefixes []string) (string, bool) {
	var tier string
	if myinfo, ok := n.shaMap[jobID]; ok {
		tier = myinfo.Tier
		return tier, true
	}
	if hasPrefix(jobID, prefixes) {
		tier = getTierFromJob(jobID)
	} else {
		return "", false
	}
	return tier, true
}

func handleCmdPs(sess ssh.Session, cmds []string, n *NomadTier, prefixes []string) {
	jobs := []string{}
	for job := range n.jobMap {
		jobs = append(jobs, job)
	}
	sort.Strings(jobs)
	h := sha1.New()
	log.Printf("%s is running ps\n", sess.User())
	w := tabwriter.NewWriter(sess, 0, 0, 1, ' ', tabwriter.Debug)
	fmt.Fprintf(w, "Exec ID\tJob/Task\tNode\tUptime\tCPU\tMem(max)\n")
	for _, job := range jobs {
		allocs := n.jobMap[job]
		if !hasPrefix(job, prefixes) {
			continue
		}
		if len(cmds) > 1 {
			if !strings.Contains(job, cmds[1]) {
				continue
			}
		}
		fmt.Fprintf(w, "          \t%s\t                \t       \t  \t\n", job)
		for _, alloc := range allocs {
			for task, _ := range alloc.TaskStates {
				h.Write([]byte(task + alloc.ID))
				hash := hex.EncodeToString(h.Sum(nil))[0:10]
				h.Reset()
				fmt.Fprintf(w, "%v\t%v\t%v\t%v\t%v\t%v(%v)\n", hash, task, n.nmap[alloc.NodeID].Name, humanize.Time(time.Unix(0, alloc.ModifyTime)), math.Floor(n.allocMap[alloc.ID].ResourceUsage.CpuStats.TotalTicks), humanize.IBytes(n.allocMap[alloc.ID].ResourceUsage.MemoryStats.RSS), humanize.IBytes(n.allocMap[alloc.ID].ResourceUsage.MemoryStats.MaxUsage))
			}
		}
	}
	w.Flush()
}

func handleCmdExec(sess ssh.Session, cmds []string, n *NomadTier) *AllocInfo {
	myinfo := n.shaMap[cmds[1]]
	if myinfo == nil {
		fmt.Fprintln(sess, "No such ExecID found!")
		return nil
	}
	if len(cmds) == 2 {
		myinfo.Command = "/bin/sh"
	} else {
		myinfo.Command = cmds[2]
	}
	return myinfo
}

func handleCmdStop(sess ssh.Session, cmds []string, n *NomadTier, prefixes []string) int {
	jobID := cmds[1]
	var tier string
	var ok bool
	if tier, ok = isJobAllowed(n, jobID, prefixes); !ok {
		fmt.Fprintf(sess, "Not authorized to stop %s\n", jobID)
		log.Printf("%s tried to stop %s\n", sess.User(), jobID)
		return 1
	}
	if tier == "" || jobID == "" {
		fmt.Fprintln(sess, "Invalid job id provided")
		log.Printf("%s tried to stop %s", sess.User(), jobID)
		return 1
	}
	res := stopJob(tier, jobID)
	if res {
		fmt.Fprintf(sess, "Job %s stopped\n", jobID)
		log.Printf("%s stopped %s succesfully", sess.User(), jobID)
		return 0
	}
	fmt.Fprintf(sess, "Job %s failed to stop\n", jobID)
	log.Printf("%s stopped %s unsuccesfully", sess.User(), jobID)
	return 1
}

func handleCmdInspect(sess ssh.Session, cmds []string, n *NomadTier, prefixes []string) int {
	jobID := cmds[1]
	var tier string
	var ok bool
	if tier, ok = isJobAllowed(n, jobID, prefixes); !ok {
		fmt.Fprintf(sess, "Not authorized to inspect %s\n", jobID)
		log.Printf("%s tried to inspect %s\n", sess.User(), jobID)
		return 1
	}
	if tier == "" || jobID == "" {
		fmt.Fprintln(sess, "Invalid job id provided")
		log.Printf("%s tried to inspect %s", sess.User(), jobID)
		return 1
	}
	res := inspectJob(tier, jobID)
	fmt.Fprintln(sess, res)
	return 0
}

func handleCmdStatus(sess ssh.Session, cmds []string, n *NomadTier, prefixes []string) int {
	if len(cmds) != 4 {
		fmt.Fprintln(sess, "Not enough arguments")
		log.Printf("%s tried to run status %#v\n", sess.User(), cmds)
		return 1
	}
	jobID := cmds[3]
	if !hasPrefix(jobID, prefixes) {
		fmt.Fprintf(sess, "Not authorized to inspect %s\n", jobID)
		log.Printf("%s tried to inspect %s\n", sess.User(), jobID)
		return 1
	}
	cmds[0] = "raw"
	return handleCmdRaw(sess, cmds, []string{"raw"})
}

func handleCmdDeployment(sess ssh.Session, cmds []string, n *NomadTier, prefixes []string) int {
	deployID := cmds[len(cmds)-1]
	if deploy, ok := n.deployMap[deployID]; ok {
		fmt.Printf("deploy %#v", deploy)
		if !hasPrefix(deploy.JobID, prefixes) {
			fmt.Fprintf(sess, "Not authorized to deployment of %s\n", deploy.JobID)
			log.Printf("%s tried to %#v\n", sess.User(), cmds)
			return 1
		}
	} else {
		switch deployID {
		case "promote", "fail":
			cmds[0] = "raw"
			return handleCmdRaw(sess, cmds, []string{"raw"})
		}
		fmt.Fprintf(sess, "Non-existing deployment ID %s\n", deployID)
		log.Printf("%s used non-existing deployment ID %s\n", sess.User(), deployID)
		return 1
	}

	if len(cmds) < 5 {
		fmt.Fprintln(sess, "Not enough arguments")
		log.Printf("%s tried to run status %#v\n", sess.User(), cmds)
		return 1
	}

	switch cmds[3] {
	case "promote", "fail":
		cmds[0] = "raw"
		return handleCmdRaw(sess, cmds, []string{"raw"})
	}

	fmt.Fprintf(sess, "Invalid command %s\n", cmds[3])
	log.Printf("%s tried to execute %v\n", sess.User(), cmds)
	return 1
}

func handleCmdRaw(sess ssh.Session, cmds []string, prefixes []string) int {
	if !hasPrefix("raw", prefixes) {
		fmt.Fprintln(sess, "Not authorized to run raw")
		log.Printf("%s tried to run raw %v", sess.User(), cmds[1:])
		return 1
	}
	log.Printf("%s is running raw %v", sess.User(), cmds[1:])
	nc := getNomadConfig()
	cmd := exec.Command(viper.GetString("general.nomadbinary"), cmds[2:]...)
	cmd.Env = append(cmd.Env, "NOMAD_ADDR="+nc[cmds[1]].URL)
	stderr, err := cmd.StderrPipe()
	if nil != err {
		log.Println("Error obtaining stderr: %s", err.Error())
		fmt.Fprintf(sess, "An error occured")
		return 1
	}
	stdout, err := cmd.StdoutPipe()
	if nil != err {
		log.Println("Error obtaining stdout: %s", err.Error())
		fmt.Fprintf(sess, "An error occured")
		return 1
	}
	reader := bufio.NewReader(stdout)
	reader2 := bufio.NewReader(stderr)
	go func(reader io.Reader) {
		io.Copy(sess, reader)
	}(reader)
	go func(reader io.Reader) {
		io.Copy(sess, reader)
	}(reader2)

	if err := cmd.Start(); nil != err {
		log.Println("Error starting program: %s, %s", cmd.Path, err.Error())
		fmt.Fprintf(sess, "An error occured")
		return 1
	}
	cmd.Wait()
	return 0
}
