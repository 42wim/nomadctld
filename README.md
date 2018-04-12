# nomadctld

nomadctld is a ssh server which sits between your users and the nomad cluster.  
This way you can give users limited access to your nomad cluster and allow them to attach, see logs, tail logs, exec containers they own on the cluster.
The server you're running nomadctld on needs to have access to all of your nomad nodes (usually on port 4646) and the docker daemon on those same hosts.
(TLS is not supported yet)

Still a WIP (works for me)

## features
* Authenticate users via ssh-keys
* Authorize users on nomad job prefixes
* Supports following commands
  * ps <filter> (shows all/filtered nomad jobs) (<filter> is optional)
  * exec <execID> <command> (executes <command> in container <execID> (command default is /bin/sh)\\n"
  * logs <execID> (shows last 100 stdout lines of <execID>)\\n"
  * tail <execID> (shows last 100 stdout lines of <execID> and keeps following them (like tail -f))\\n"
  * stop <jobID> (stops a nomad job with <jobID> (<jobID> is the jobname))\\n"
  * inspect <jobID> (inspects a nomad job with <jobID> (<jobID> is the jobname))\\n"
  * raw <production/quality> <nomad command> (executes a "raw" nomad command (basically talks to the nomad binary))

## running
nomadctld looks for a nomadctld.toml in the current directory or in /etc/nomadctld

```
./nomadctld
2018/04/12 23:54:53 starting ssh server on  :2222 Using  /etc/ssh/ssh_host_rsa_key
```

You can now do a ssh -p2222 localhost ps to see all running jobs

There's also a `nomadctl` bash script included that wraps ssh, modify the first line `nomadctld="/usr/bin/ssh -p2222 yourserver"` to your own server and optionally specify your sshkey

## example
Filter for a mattermost job

```
$ nomadctl ps mattermost
Exec ID    |Job/Task         |Node      |Uptime      |CPU |Mem(max)
           |q-lnx-mattermost |          |            |    |
f429aef310 |q-lnx-mattermost |q-node-10 |3 weeks ago |2   |34 MiB(79 MiB)
           |p-lnx-mattermost |          |            |    |
e7c3629baf |p-lnx-mattermost |p-node-2  |1 week ago  |4   |56 MiB(201 MiB)
```

Get a shell on our quality container
```
$ nomadctl exec f429aef310
Welcome Wim (wim, ssh-ed25519 AAAAC3NzaC1lZDI1NT)
You have access to [p- q- t- raw]
welcome to q-lnx-mattermost on q-node-10
sh-4.2# ps -ux
USER        PID %CPU %MEM    VSZ   RSS TTY      STAT START   TIME COMMAND
root          1  0.0  0.0  11640  1344 ?        Ss   Mar21   0:00 /bin/bash /docker-entry.sh
root          8  0.0  0.2 455808 43488 ?        Sl   Mar21  22:40 ./platform --config /config_docker.json
root         31  1.0  0.0  11772  1672 ?        Ss   00:36   0:00 /bin/sh
root         38  0.0  0.0  47448  1664 ?        R+   00:36   0:00 ps -ux
sh-4.2# exit
$
```

## example configuration
```
[general]
hostkey="/etc/ssh/ssh_host_rsa_key"
#this is the port your docker daemon is listening on
dockerport="4243"
#specify the api, if you're using an older docker (1.24 is needed for centos7)
dockerapi="1.24"
nomadbinary="/usr/bin/nomad"
bind=":2222"

#create some aliases
[alias]
psp="ps p-"
psq="ps q-"
pst="ps t-"
#the production and quality below must match the nomad.production name
status="raw production status"
qstatus="raw quality status"

[nomad]
[nomad.production]
url="http://nomad.service.consul:4646"
#the prefix that production jobs start with, eg p-team1-teamjob
prefix=["p-"]

[nomad.quality]
url="http://nomad.service.qconsul:4646"
#the prefix that quality jobs start with, eg q-team1-teamjob
#we're running quality and test on the same cluster, so t- also runs here
prefix=["q-","t-"]

#prefixes are used for acl
[prefix]
#create a lnx prefix that has access to jobs starting with "p-","q-","t-" 
#and has access to the "raw" command
[prefix.lnx]
prefix=["p-","q-","t-",raw"]

#the team1 prefix has less privileges, it can only see there own jobs
[prefix.team1]
prefix=["p-team1-","q-team1-","t-team1-"]

#create some users
[users]
[users.wim]
#key is the public sshkey
key="ssh-ed25519 ABCEC4NzaC1lZDI2NTE5AADAIB3m1na7Lu74koUa5EYPvEk7xCXiwtToar73gl9w9MND"
#which prefix can this user access
prefix=["lnx"]
name="Wim"

[users.user2]
key="ssh-ed25519 ABCEC4NzaC1lZDI2NTE5AADAIB3m1na7Lu74koUa5EYPvEk7xCXiwtToar73gl9abcde"
prefix=["team2"]
name="user2"
```
