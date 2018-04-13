package main

import (
	"fmt"
	"log"
	"net"
	"strings"

	"github.com/docker/docker/api/types/container"
	"github.com/gliderlabs/ssh"
	"github.com/spf13/viper"
	gossh "golang.org/x/crypto/ssh"
)

func main() {
	readconfig()
	ssh.Handle(func(sess ssh.Session) {
		logsfollow := false
		cmds := sess.Command()
		if !validCmd(sess, cmds) {
			sess.Exit(1)
			return
		}
		authorizedKey := gossh.MarshalAuthorizedKey(sess.PublicKey())
		if len(authorizedKey) == 0 {
			fmt.Fprintf(sess, "Key needed")
			sess.Exit(1)
			return
		}
		user := checkKey(string(authorizedKey))
		userPrefix := user.Prefix
		log.Println("prefixes found:", userPrefix)
		if len(userPrefix) == 0 {
			log.Println("%s connected with unauthorized key %s", sess.User(), string(authorizedKey))
		}

		var myinfo *AllocInfo
		var ni NomadInfo
		var n *NomadTier

		if cmds[0] != "raw" {
			ni = getNomadInfo()
			n = ni["alles"]
		}

		if len(cmds) > 0 {
			for alias, aliascmd := range viper.GetStringMapString("alias") {
				if cmds[0] == alias {
					newcmds := []string{}
					newcmds = append(newcmds, strings.Fields(aliascmd)...)
					newcmds = append(newcmds, cmds[1:]...)
					cmds = newcmds
				}
			}

			switch cmds[0] {
			case "ps":
				handleCmdPs(sess, cmds, n, userPrefix)
				return
			case "exec":
				myinfo = handleCmdExec(sess, cmds, n)
			case "stop":
				sess.Exit(handleCmdStop(sess, cmds, n, userPrefix))
				return
			case "inspect":
				sess.Exit(handleCmdInspect(sess, cmds, n, userPrefix))
				return
			case "attach", "logs":
				myinfo = n.shaMap[cmds[1]]
			case "tail":
				myinfo = n.shaMap[cmds[1]]
				logsfollow = true
			case "raw":
				sess.Exit(handleCmdRaw(sess, cmds, userPrefix))
				return
			}
			if myinfo == nil {
				sess.Exit(1)
				return
			}

			// check auth
			if myinfo != nil {
				if !hasPrefix(myinfo.JobID, userPrefix) {
					fmt.Fprintf(sess, "Not authorized to %s %s\n", cmds[0], cmds[1])
					log.Printf("%s tried to %s %s (%s)", sess.User(), cmds[0], cmds[1], myinfo.JobID)
					sess.Exit(1)
					return
				}
			}
			if cmds[0] == "exec" {
				fmt.Fprintf(sess, "Welcome %s (%s, %s)\nYou have access to %v\n", user.Name, user.ID, user.Key[0:30], userPrefix)
			}
		}

		_, _, isTty := sess.Pty()
		cfg := &container.Config{
			Tty:          isTty,
			AttachStderr: true,
			AttachStdin:  true,
			AttachStdout: true,
		}

		var status int
		var err error
		myinfo.DockerHost = "tcp://" + net.JoinHostPort(myinfo.Node.Name, viper.GetString("general.dockerport"))
		switch cmds[0] {
		case "exec":
			log.Printf("%s running exec on %#v\n", sess.User(), myinfo)
			status, err = dockerExec(cfg, sess, myinfo)
		case "attach":
			log.Printf("%s running attach on %#v\n", sess.User(), myinfo)
			status, err = dockerAttach(cfg, sess, myinfo)
		case "logs", "tail":
			log.Printf("%s running logs on %#v\n", sess.User(), myinfo)
			status, err = dockerLogs(cfg, sess, myinfo, logsfollow)
		}
		if err != nil {
			log.Println(err)
		}
		sess.Exit(int(status))
	})

	publicKeyOption := ssh.PublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
		return true // allow all keys
	})

	log.Println("starting ssh server on ", viper.GetString("general.bind"), "Using ", viper.GetString("general.hostkey"))
	log.Fatal(ssh.ListenAndServe(viper.GetString("general.bind"), nil, publicKeyOption, ssh.HostKeyFile(viper.GetString("general.hostkey"))))
}
