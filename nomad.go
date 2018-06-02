package main

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"log"
	"sync"

	"github.com/hashicorp/nomad/api"
	"github.com/ugorji/go/codec"
)

type NomadInfo map[string]*NomadTier
type TaskGroupMap map[string][]*api.AllocationListStub

type NomadTier struct {
	Name      string
	Alias     []string
	Prefix    []string
	jobMap    map[string][]*api.AllocationListStub
	nmap      map[string]*api.Node
	shaMap    map[string]*AllocInfo
	allocMap  map[string]*api.AllocResourceUsage
	deployMap map[string]*api.Deployment // key is deployment ID
	sync.RWMutex
}

type NomadConfig struct {
	Name   string
	URL    string
	Alias  []string
	Prefix []string
}

type AllocInfo struct {
	Name       string
	ID         string
	Command    string
	Node       *api.Node
	DockerHost string
	JobID      string
	Tier       string
}

func stopJob(tier, jobid string) bool {
	cfg := getNomadConfig()
	info := cfg[tier]
	c, _ := api.NewClient(&api.Config{Address: info.URL})
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
	c, _ := api.NewClient(&api.Config{Address: info.URL})
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
	c, _ := api.NewClient(&api.Config{Address: info.URL})
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
	c, _ := api.NewClient(&api.Config{Address: info.URL})
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
	for tier, nc := range cfg {
		i[tier] = getNomadTierInfo(tier, nc.URL, nc.Alias, nc.Prefix)
	}
	n := &NomadTier{jobMap: make(map[string][]*api.AllocationListStub), nmap: make(map[string]*api.Node), shaMap: make(map[string]*AllocInfo), allocMap: make(map[string]*api.AllocResourceUsage), deployMap: make(map[string]*api.Deployment)}
	i["alles"] = n

	for tier, _ := range cfg {
		for k, v := range i[tier].jobMap {
			i["alles"].jobMap[k] = v
		}
		for k, v := range i[tier].nmap {
			i["alles"].nmap[k] = v
		}
		for k, v := range i[tier].shaMap {
			i["alles"].shaMap[k] = v
		}
		for k, v := range i[tier].allocMap {
			i["alles"].allocMap[k] = v
		}
		for k, v := range i[tier].deployMap {
			i["alles"].deployMap[k] = v
		}
	}
	return i
}

func getNomadTierInfo(tier, url string, alias []string, prefix []string) *NomadTier {
	n := &NomadTier{Name: tier,
		Alias:     alias,
		Prefix:    prefix,
		jobMap:    make(map[string][]*api.AllocationListStub),
		nmap:      make(map[string]*api.Node),
		shaMap:    make(map[string]*AllocInfo),
		allocMap:  make(map[string]*api.AllocResourceUsage),
		deployMap: make(map[string]*api.Deployment)}
	c, _ := api.NewClient(&api.Config{Address: url})
	allocs, _, _ := c.Allocations().List(nil)
	nodes, _, _ := c.Nodes().List(nil)
	deploys, _, _ := c.Deployments().List(nil)

	// TODO we can go back to stub when running 0.8 which has Address information in the stub
	// something we should also cache and run every x minute instead on every call
	var wg sync.WaitGroup
	for _, node := range nodes {
		wg.Add(1)
		go func(node *api.NodeListStub, n *NomadTier) {
			realNode, _, err := c.Nodes().Info(node.ID, nil)
			if err != nil {
				log.Println("getNomadTierInfo:", err)
				wg.Done()
				return
			}
			n.Lock()
			n.nmap[node.ID] = realNode
			n.Unlock()
			wg.Done()
		}(node, n)
	}
	wg.Wait()

	for _, deploy := range deploys {
		n.deployMap[deploy.ID] = deploy
	}

	for _, alloc := range allocs {
		if alloc.ClientStatus == "running" {
			n.jobMap[alloc.JobID] = append(n.jobMap[alloc.JobID], alloc)
		}
	}

	h := sha1.New()
	for _, allocs := range n.jobMap {
		for _, alloc := range allocs {
			for k, _ := range alloc.TaskStates {
				h.Write([]byte(k + alloc.ID))
				hash := hex.EncodeToString(h.Sum(nil))[0:10]
				n.shaMap[hash] = &AllocInfo{Name: k, ID: alloc.ID, Node: n.nmap[alloc.NodeID], JobID: alloc.JobID, Tier: tier}
				h.Reset()
			}
		}
	}

	//var wg sync.WaitGroup
	for _, allocs := range n.jobMap {
		for _, alloc := range allocs {
			wg.Add(1)
			go func(alloc *api.AllocationListStub, n *NomadTier) {
				realalloc, _, err := c.Allocations().Info(alloc.ID, nil)
				if err != nil {
					log.Println("realloc", err)
				}
				//log.Printf("NODE: %#v\n", n.nmap[alloc.NodeID])
				c, _ := api.NewClient(&api.Config{Address: "http://" + n.nmap[alloc.NodeID].HTTPAddr})
				stats, err := c.Allocations().Stats(realalloc, nil)
				if err != nil {
					log.Println("stats", err)
				}
				n.Lock()
				n.allocMap[alloc.ID] = stats
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
