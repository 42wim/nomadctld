# nomadctld

nomadctld is a ssh server which sits between your users and the nomad cluster.  
This way you can give users limited access to your nomad cluster and allow them to attach, see logs, tail logs, stop, restart, exec containers they own on the cluster.
The server you're running nomadctld on needs to have access to your nomad server (usually on port 4646) and the docker daemon on all nomad nodes.
(TLS is not supported yet)

## features
* Support nomad ACL tokens
* Authenticate users via ssh-keys
* Authorize users on nomad job prefixes
* Supports following commands
  * ps <filter> (shows all/filtered nomad jobs) (<filter> is optional)
  * batch <filter> (shows batch nomad jobs) (<filter> is optional)
  * exec <execID> <command> (executes <command> in container <execID> (command default is /bin/sh)
  * logs <execID> (shows last 100 stdout lines of <execID>)
  * tail <execID> (shows last 100 stdout lines of <execID> and keeps following them (like tail -f))
  * stop <jobID> (stops a nomad job with <jobID> (<jobID> is the jobname))
  * status <jobID> (shows status of a nomad job with <jobID> (<jobID> is the jobname)
  * inspect <jobID> (inspects a nomad job with <jobID> (<jobID> is the jobname))
  * di <execID> (runs equivalent of docker inspect on container <execID>
  * raw <production/quality> <nomad command> (executes a "raw" nomad command (basically talks to the nomad binary))
  * restart <jobID> (restart a job)
  * pstree <filter> (shows all/filtered nomad jobs)
  * info <jobID> (shows information about all allocations of the job)

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

Show batch jobs

```
$ nomadctl batch
Job ID                                           |Next        |                          |Config
p-es-log-curator                                 |5m24s       |2018-07-12T15:52:00+02:00 |52 6,15,18 * * * *
p-iss-frontend-sync                              |9m24s       |2018-07-12T15:56:00+02:00 |11/15 * * * * *
```


## debugging examples

If you see a OK or a FAIL next to the job, this means the deployment is OK or has FAILed.
A deployment can fail even when the containers are running, because of allocations that took too much time to become healthy

### Extra column legend

* F = allocation failed
* P = allocation pending
* T = allocation has been terminated
* O = allocation has been terminated by OOM
* NR = allocation will not restart
* KV = allocation has been killed because of vault issues
* U = allocation is unhealthy (based on consul checks)

### With failures
```
Exec ID    |Job/Task                                     |Node      |Uptime        |CPU |Mem(max)          |Extra
076f1388af |p-lnx-helloworld2                            |p-node-5  |1 hour ago    |0   |0 B(0 B)          |F,KV,U
30a1e62b4e |p-es-iss-cluster1-frontend                   |p-node-9  |14 hours ago  |4   |1.9 GiB(2.0 GiB)  |O,O,O
b4eaf79c05 |q-fii-lamp                                   |q-node-11 |1 day ago     |0   |39 MiB(67 MiB)    |U,T
f3c6ee4200 |q-fii-monitoring-wmsmonitor                  |q-node-11 |17 hours ago  |0   |22 MiB(43 MiB)    |T
```
* p-lnx-helloworld2 allocations have failed because of vault issues and the allocation is unhealthy
* p-es-iss-cluster1-frontend is 3 times terminated because of a OOM issue (but is still running because no F)
* q-fii-monitoring-wmsmonitor has been terminated once (but is still running because no F)
* q-fii-lamp been terminated once and is now unhealthy (but is still running because no F)

Further debugging of those failures:

```
$ nomadctl info p-lnx-helloworld2

076f1388af |failed          |1 hour ago |p-lnx-helloworld2-main/p-lnx-helloworld2
           |p-node-5        |0 Mhz      |0 B(0 B)
           |1 hour ago      |Received   |Task received by client
           |1 hour ago      |Task Setup |Building Task Directory
           |1 hour ago      |Killing    |vault: server error deriving vault token: Error making API request.

URL: POST https://vault.service.consul:8200/v1/auth/token/create/nomad-cluster
Code: 400. Errors:

* token policies ([default p-lnx-ros]) must be subset of the role's allowed policies ([default p-es-ro p-lnx-ro])
           |1 hour ago      |Alloc Unhealthy |Unhealthy because of failed task
```
We can see that there is an issue with our vault policy


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
status="raw alles status"

[nomad]
[nomad.production]
url="http://nomad.service.consul:4646"
#the prefix that production jobs start with, eg p-team1-teamjob
prefix=["p-"]
#file containing the nomad acl token with admin privileges
token="/etc/nomadctld/token-p"

[nomad.quality]
url="http://nomad.service.qconsul:4646"
#the prefix that quality jobs start with, eg q-team1-teamjob
#we're running quality and test on the same cluster, so t- also runs here
prefix=["q-"]
token="/etc/nomadctld/token-q"

[nomad.test]
url="http://nomad.service.tconsul:4646"
#the prefix that test jobs start with, eg t-team1-teamjob
prefix=["t-"]
token="/etc/nomadctld/token-t"


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
