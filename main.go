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
		// assertion check
		if _, ok := sess.Context().Value("founduser").(*UserInfo); !ok {
			sess.Exit(1)
			return
		}
		user := sess.Context().Value("founduser").(*UserInfo)
		logsfollow := false
		cmds := sess.Command()
		if !validCmd(sess, cmds) {
			sess.Exit(1)
			return
		}
		userPrefix := user.Prefix
		log.Println("prefixes found:", userPrefix)

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
				n = ni["alles"]
				handleCmdPs(sess, cmds, n, userPrefix)
				return
			case "pst":
				n = ni["test"]
				handleCmdPs(sess, cmds, n, userPrefix)
				return
			case "psq":
				n = ni["quality"]
				handleCmdPs(sess, cmds, n, userPrefix)
				return
			case "psp":
				n = ni["production"]
				handleCmdPs(sess, cmds, n, userPrefix)
				return
			case "pstree":
				handleCmdPsTree(sess, cmds, n, userPrefix)
				return
			case "exec":
				myinfo = handleCmdExec(sess, cmds, n)
			case "stop":
				sess.Exit(handleCmdStop(sess, cmds, n, userPrefix))
				return
			case "restart":
				sess.Exit(handleCmdRestart(sess, cmds, n, userPrefix))
				return
			case "inspect":
				sess.Exit(handleCmdInspect(sess, cmds, n, userPrefix))
				return
			case "attach", "logs", "di", "tcpdump":
				myinfo = n.shaMap[cmds[1]]
			case "tail":
				myinfo = n.shaMap[cmds[1]]
				logsfollow = true
			case "rawl":
				if len(cmds) < 3 {
					return
				}
				switch cmds[2] {
				case "deployment":
					handleCmdDeployment(sess, cmds, n, userPrefix)
				case "status":
					handleCmdStatus(sess, cmds, n, userPrefix)
				}
			case "raw":
				sess.Exit(handleCmdRaw(sess, cmds, userPrefix))
				return
			case "ipset":
				myinfo = n.shaMap[cmds[1]]
				if len(cmds) == 3 {
					if !handleCmdIpsetAdd(sess, cmds) {
						return
					}
				}
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
		case "di":
			log.Printf("%s running docker inspect on %#v\n", sess.User(), myinfo)
			status, err = dockerInspect(cfg, sess, myinfo)
		case "tcpdump":
			log.Printf("%s running docker tcpdump on %#v\n", sess.User(), myinfo)
			status, err = dockerTcpDump(cfg, sess, myinfo)
		case "ipset":
			if len(cmds) == 3 {
				log.Printf("%s running docker ipset on %#v\n", sess.User(), myinfo)
				status, err = dockerIpsetAdd(cfg, sess, myinfo, cmds[2])
			} else {
				log.Printf("%s running docker ipset on %#v\n", sess.User(), myinfo)
				status, err = dockerIpset(cfg, sess, myinfo)
			}
		}
		if err != nil {
			log.Println(err)
		}
		sess.Exit(int(status))
	})

	publicKeyOption := ssh.PublicKeyAuth(func(ctx ssh.Context, key ssh.PublicKey) bool {
		// if we have our user, return
		if ctx.Value("founduser") != nil {
			return true
		}
		log.Println("Connection from", ctx.User(), ctx.RemoteAddr().String(), string(gossh.MarshalAuthorizedKey(key)))
		authorizedKey := gossh.MarshalAuthorizedKey(key)
		user := checkKey(string(authorizedKey), ctx.User())
		// if the user has prefixes we found the correct one
		if len(user.Prefix) > 0 {
			ctx.SetValue("founduser", user)
			return true
		}
		return false
	})

	log.Println("starting ssh server on ", viper.GetString("general.bind"), "Using ", viper.GetString("general.hostkey"))
	log.Fatal(ssh.ListenAndServe(viper.GetString("general.bind"), nil, publicKeyOption, ssh.HostKeyFile(viper.GetString("general.hostkey"))))
}
