package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"log"
	"sync"
	"time"

	"github.com/hashicorp/nomad/api"
	"github.com/ugorji/go/codec"
)

type NomadInfo map[string]*NomadTier
type TaskGroupMap map[string][]*api.AllocationListStub

type NomadTier struct {
	Name         string
	Alias        []string
	Prefix       []string
	allocStubMap map[string][]*api.AllocationListStub
	jobStubMap   map[string]*api.JobListStub
	jobMap       map[string]*api.Job
	nmap         map[string]*api.NodeListStub
	shaMap       map[string]*AllocInfo
	statsMap     map[string]*api.AllocResourceUsage
	deployMap    map[string]*api.Deployment // key is deployment ID
	sync.RWMutex
}

type NomadConfig struct {
	Name   string
	URL    string
	Token  string
	Alias  []string
	Prefix []string
}

type AllocInfo struct {
	Name       string
	ID         string
	Command    string
	Node       *api.NodeListStub
	DockerHost string
	JobID      string
	Tier       string
}

func stopJob(tier, jobid string) bool {
	cfg := getNomadConfig()
	info := cfg[tier]
	c, _ := api.NewClient(&api.Config{Address: info.URL, SecretID: info.Token})
	_, _, err := c.Jobs().Info(jobid, nil)
	if err != nil {
		log.Println(err)
		return false
	}
	res, _, err := c.Jobs().Deregister(jobid, false, nil)
	if err != nil {
		log.Println(res, err)
		return false
	}
	log.Println(res)
	return true
}

func restartJob(tier, jobid string) bool {
	cfg := getNomadConfig()
	info := cfg[tier]
	c, _ := api.NewClient(&api.Config{Address: info.URL, SecretID: info.Token})
	job, _, err := c.Jobs().Info(jobid, nil)
	if err != nil {
		log.Println(err)
		return false
	}
	res, _, err := c.Jobs().Deregister(jobid, false, nil)
	if err != nil {
		log.Println(res, err)
		return false
	}
	stop := false
	job.Stop = &stop
	time.Sleep(time.Second * 5)
	_, _, err = c.Jobs().Register(job, nil)
	if err != nil {
		log.Println(err)
		return false
	}
	log.Println(res)
	return true
}

func restartTaskGroup(tier, jobid string, group string) bool {
	cfg := getNomadConfig()
	info := cfg[tier]
	c, _ := api.NewClient(&api.Config{Address: info.URL, SecretID: info.Token})
	job, _, err := c.Jobs().Info(jobid, nil)
	if err != nil {
		log.Println(err)
		return false
	}

	for _, tg := range job.TaskGroups {
		if *tg.Name == group {
			prev := tg.Count

			tg.Count = new(int)
			_, _, err := c.Jobs().Register(job, nil)
			if err != nil {
				log.Println(err)
				return false
			}

			// set to previous value
			tg.Count = prev
			_, _, err = c.Jobs().Register(job, nil)
			if err != nil {
				log.Println(err)
				return false
			}
			return true
		}
	}
	log.Println(jobid, "/", group, "not found")
	return false
}

func inspectJob(tier, jobid string) string {
	var jsonHandlePretty = &codec.JsonHandle{
		HTMLCharsAsIs: true,
		Indent:        4,
	}

	cfg := getNomadConfig()
	info := cfg[tier]
	c, _ := api.NewClient(&api.Config{Address: info.URL, SecretID: info.Token})
	job, _, err := c.Jobs().Info(jobid, nil)
	if err != nil {
		log.Println(err)
	}
	req := api.RegisterJobRequest{Job: job}
	var buf bytes.Buffer
	enc := codec.NewEncoder(&buf, jsonHandlePretty)
	err = enc.Encode(req)
	if err != nil {
		log.Println(err)
	}
	return buf.String()
}

func getNomadInfo() NomadInfo {
	i := make(NomadInfo)
	cfg := getNomadConfig()
	ntier := make(chan *NomadTier)
	for tier, nc := range cfg {
		go func(tier string, nc *NomadConfig) {
			ntier <- getNomadTierInfo(tier, nc.URL, nc.Token, nc.Alias, nc.Prefix)
		}(tier, nc)
	}
	for res := range ntier {
		i[res.Name] = res
		if len(i) == len(cfg) {
			break
		}
	}
	close(ntier)

	n := &NomadTier{allocStubMap: make(map[string][]*api.AllocationListStub), jobStubMap: make(map[string]*api.JobListStub), jobMap: make(map[string]*api.Job), nmap: make(map[string]*api.NodeListStub), shaMap: make(map[string]*AllocInfo), statsMap: make(map[string]*api.AllocResourceUsage), deployMap: make(map[string]*api.Deployment)}
	i["alles"] = n
	i["alles"].Name = "alles"

	for tier, _ := range cfg {
		for k, v := range i[tier].allocStubMap {
			i["alles"].allocStubMap[k] = v
		}
		for k, v := range i[tier].jobStubMap {
			i["alles"].jobStubMap[k] = v
		}
		for k, v := range i[tier].jobMap {
			i["alles"].jobMap[k] = v
		}
		for k, v := range i[tier].nmap {
			i["alles"].nmap[k] = v
		}
		for k, v := range i[tier].shaMap {
			i["alles"].shaMap[k] = v
		}
		for k, v := range i[tier].statsMap {
			i["alles"].statsMap[k] = v
		}
		for k, v := range i[tier].deployMap {
			i["alles"].deployMap[k] = v
		}
	}
	return i
}

func getNomadTierInfo(tier, url, token string, alias []string, prefix []string) *NomadTier {
	n := &NomadTier{Name: tier,
		Alias:        alias,
		Prefix:       prefix,
		allocStubMap: make(map[string][]*api.AllocationListStub),
		jobStubMap:   make(map[string]*api.JobListStub),
		jobMap:       make(map[string]*api.Job),
		nmap:         make(map[string]*api.NodeListStub),
		shaMap:       make(map[string]*AllocInfo),
		statsMap:     make(map[string]*api.AllocResourceUsage),
		deployMap:    make(map[string]*api.Deployment)}
	c, err := api.NewClient(&api.Config{Address: url, SecretID: token})
	if err != nil {
		log.Printf("getNomadTierInfo err: %s %#v\n", url, err.Error())
	}
	allocs, _, err := c.Allocations().List(nil)
	if err != nil {
		log.Printf("getNomadTierInfo allocs err: %s %#v\n", url, err.Error())
	}
	nodes, _, err := c.Nodes().List(nil)
	if err != nil {
		log.Printf("getNomadTierInfo nodes err: %s %#v\n", url, err.Error())
	}
	deploys, _, err := c.Deployments().List(nil)
	if err != nil {
		log.Printf("getNomadTierInfo deployments err: %s %#v\n", url, err.Error())
	}
	jobStubs, _, err := c.Jobs().List(nil)
	if err != nil {
		log.Printf("getNomadTierInfo jobs err: %s %#v\n", url, err.Error())
	}

	var wg sync.WaitGroup
	for _, node := range nodes {
		n.nmap[node.ID] = node
	}

	for _, jobStub := range jobStubs {
		n.jobStubMap[jobStub.ID] = jobStub
		if jobStub.Periodic {
			j, _, _ := c.Jobs().Info(jobStub.ID, nil)
			n.jobMap[jobStub.ID] = j
		}
	}

	for _, deploy := range deploys {
		n.deployMap[deploy.ID] = deploy
	}

	for _, alloc := range allocs {
		if alloc.ClientStatus == "running" || alloc.ClientStatus == "pending" || alloc.ClientStatus == "failed" {
			n.allocStubMap[alloc.JobID] = append(n.allocStubMap[alloc.JobID], alloc)
		}
	}

	h := sha1.New()
	for _, allocs := range n.allocStubMap {
		for _, alloc := range allocs {
			for k, _ := range alloc.TaskStates {
				h.Write([]byte(k + alloc.ID))
				hash := hex.EncodeToString(h.Sum(nil))[0:10]
				n.shaMap[hash] = &AllocInfo{Name: k, ID: alloc.ID, Node: n.nmap[alloc.NodeID], JobID: alloc.JobID, Tier: tier}
				h.Reset()
			}
		}
	}

	for _, allocs := range n.allocStubMap {
		for _, alloc := range allocs {
			wg.Add(1)
			go func(alloc *api.AllocationListStub, n *NomadTier) {
				realalloc, _, err := c.Allocations().Info(alloc.ID, nil)
				if err != nil {
					log.Println("realloc", err)
				}
				//log.Printf("NODE: %#v\n", n.nmap[alloc.NodeID])
				//c, _ := api.NewClient(&api.Config{Address: "http://" + n.nmap[alloc.NodeID].HTTPAddr})
				stats, err := c.Allocations().Stats(realalloc, nil)
				if err != nil {
					log.Println("stats", err)
				}
				n.Lock()
				n.statsMap[alloc.ID] = stats
				n.Unlock()
				wg.Done()
			}(alloc, n)
		}
	}
	wg.Wait()
	return n
}

func getTierFromJob(job string) string {
	cfg := getNomadConfig()
	for tier, info := range cfg {
		if hasPrefix(job, info.Prefix) {
			return tier
		}
	}
	return "unset"
}

// formatTime formats the time to string based on RFC822
func formatTime(t time.Time) string {
	if t.Unix() < 1 {
		// It's more confusing to display the UNIX epoch or a zero value than nothing
		return ""
	}
	// Return ISO_8601 time format GH-3806
	return t.Format("2006-01-02T15:04:05Z07:00")
}

// formatUnixNanoTime is a helper for formatting time for output.
func formatUnixNanoTime(nano int64) string {
	t := time.Unix(0, nano)
	return formatTime(t)
}

// formatTimeDifference takes two times and determines their duration difference
// truncating to a passed unit.
// E.g. formatTimeDifference(first=1m22s33ms, second=1m28s55ms, time.Second) -> 6s
func formatTimeDifference(first, second time.Time, d time.Duration) string {
	return second.Truncate(d).Sub(first.Truncate(d)).String()
}
