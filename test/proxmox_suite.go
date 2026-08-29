// Mock Proxmox VE cluster and scenarios. Drives the proxmox module through the
// guest lifecycle: ticket and token authentication, node selection, VMID
// allocation, container creation, template cloning, the plan change that has to
// preserve the guest's hardware address, and termination.
//
// The pending-clone scenario deliberately lets the module exhaust its task-poll
// budget, so it takes as long as that budget allows.
package main

import (
	"fmt"
	"net/url"
	"strings"
)

type pmxGuest struct {
	Node   string
	Type   string
	Status string
	Config map[string]any
}

type pmxCluster struct {
	guests  map[int]*pmxGuest
	tasks   map[string]string // upid -> exitstatus, "running" while unfinished
	taskSeq int

	tickets    int
	lastCreate map[string]any
	lastClone  map[string]any
	lastConfig map[string]any
	lastResize map[string]any
	stopped    []int
	started    []int
	destroyed  []int
	rebooted   []int
	agentUp    bool
}

func newPmx() *pmxCluster {
	return &pmxCluster{
		guests: map[int]*pmxGuest{
			9000: {Node: "pve1", Type: "qemu", Status: "stopped", Config: map[string]any{
				"net0": "virtio=AA:BB:CC:11:22:33,bridge=vmbr0",
			}},
		},
		tasks: map[string]string{},
	}
}

// task registers a background task and returns the UPID Proxmox would answer a
// mutating call with. An exit of "running" never completes.
func (c *pmxCluster) task(node, exit string) string {
	c.taskSeq++
	upid := fmt.Sprintf("UPID:%s:0000A%03d:00B1:66C0:qmcreate:vps:root@pam:", node, c.taskSeq)
	c.tasks[upid] = exit
	return upid
}

func (c *pmxCluster) route(method, rawURL string, body map[string]any) (int, string) {
	u, _ := url.Parse(rawURL)
	ep := u.Path[strings.Index(u.Path, "/api2/json/")+len("/api2/json/"):]
	seg := strings.Split(ep, "/")

	switch {
	case method == "POST" && ep == "access/ticket":
		c.tickets++
		if str(body["username"]) != "root@pam" || str(body["password"]) != "pve-secret" {
			return 401, `{"data":null}`
		}
		return 200, `{"data":{"ticket":"PVE:root@pam:0123","CSRFPreventionToken":"0123:abcd"}}`

	case method == "GET" && ep == "version":
		return 200, `{"data":{"version":"8.2.2","release":"8.2"}}`

	case method == "GET" && ep == "nodes":
		return 200, `{"data":[
			{"node":"pve1","status":"online","maxmem":16000000000,"mem":12000000000},
			{"node":"pve2","status":"online","maxmem":32000000000,"mem":8000000000},
			{"node":"pve3","status":"offline","maxmem":64000000000,"mem":0}
		]}`

	case method == "GET" && ep == "cluster/nextid":
		return 200, `{"data":"105"}`

	case method == "GET" && pathHead(ep) == "cluster" && strings.HasPrefix(ep, "cluster/resources"):
		return 200, c.resources()

	case method == "GET" && len(seg) == 5 && seg[2] == "tasks" && seg[4] == "status":
		exit, ok := c.tasks[seg[3]]
		if !ok {
			return 404, `{"data":null}`
		}
		if exit == "running" {
			return 200, `{"data":{"status":"running"}}`
		}
		return 200, fmt.Sprintf(`{"data":{"status":"stopped","exitstatus":%q}}`, exit)

	// POST /nodes/{node}/{qemu|lxc}
	case method == "POST" && len(seg) == 3 && seg[0] == "nodes":
		c.lastCreate = body
		vmid := toInt(body["vmid"])
		status := "stopped"
		if str(body["start"]) == "1" {
			status = "running"
		}
		c.guests[vmid] = &pmxGuest{Node: seg[1], Type: seg[2], Status: status, Config: body}
		return 200, fmt.Sprintf(`{"data":%q}`, c.task(seg[1], "OK"))

	// POST /nodes/{node}/{type}/{vmid}/clone
	case method == "POST" && len(seg) == 5 && seg[4] == "clone":
		c.lastClone = body
		node := seg[1]
		if t := str(body["target"]); t != "" {
			node = t
		}
		newid := toInt(body["newid"])
		exit := "OK"
		if newid == pmxPendingVMID {
			exit = "running"
		}
		c.guests[newid] = &pmxGuest{Node: node, Type: seg[2], Status: "stopped", Config: map[string]any{
			"net0": "virtio=DD:EE:FF:44:55:66,bridge=vmbr0",
		}}
		return 200, fmt.Sprintf(`{"data":%q}`, c.task(seg[1], exit))

	// GET|POST|PUT /nodes/{node}/{type}/{vmid}/config
	case len(seg) == 5 && seg[4] == "config":
		g := c.guests[atoiSafe(seg[3])]
		if g == nil {
			return 500, `{"errors":{"vmid":"no such guest"}}`
		}
		if method == "GET" {
			return 200, jsonObject(g.Config)
		}
		if method == "PUT" && g.Type != "lxc" {
			return 501, `{"errors":{"method":"PUT is the container endpoint"}}`
		}
		if method == "POST" && g.Type != "qemu" {
			return 501, `{"errors":{"method":"POST is the vm endpoint"}}`
		}
		c.lastConfig = body
		for k, v := range body {
			g.Config[k] = v
		}
		return 200, `{"data":null}`

	// POST /nodes/{node}/{type}/{vmid}/status/{action}
	case method == "POST" && len(seg) == 6 && seg[4] == "status":
		vmid := atoiSafe(seg[3])
		g := c.guests[vmid]
		if g == nil {
			return 500, `{"errors":{"vmid":"no such guest"}}`
		}
		switch seg[5] {
		case "start":
			g.Status = "running"
			c.started = append(c.started, vmid)
		case "stop", "shutdown":
			g.Status = "stopped"
			c.stopped = append(c.stopped, vmid)
		case "reboot":
			c.rebooted = append(c.rebooted, vmid)
		}
		return 200, fmt.Sprintf(`{"data":%q}`, c.task(seg[1], "OK"))

	case method == "GET" && len(seg) == 5 && seg[4] == "interfaces":
		if c.guests[atoiSafe(seg[3])] == nil {
			return 500, `{"errors":{"vmid":"no such guest"}}`
		}
		return 200, `{"data":[{"name":"lo","inet":"127.0.0.1/8"},{"name":"eth0","inet":"198.51.100.24/24"}]}`

	case method == "GET" && len(seg) == 6 && seg[4] == "agent":
		if !c.agentUp {
			return 500, `{"errors":{"agent":"QEMU guest agent is not running"}}`
		}
		return 200, `{"data":{"result":[
			{"name":"lo","ip-addresses":[{"ip-address":"127.0.0.1","ip-address-type":"ipv4"}]},
			{"name":"ens18","ip-addresses":[{"ip-address":"fe80::1","ip-address-type":"ipv6"},{"ip-address":"198.51.100.77","ip-address-type":"ipv4"}]}
		]}}`

	case method == "PUT" && len(seg) == 5 && seg[4] == "resize":
		if c.guests[atoiSafe(seg[3])] == nil {
			return 500, `{"errors":{"vmid":"no such guest"}}`
		}
		c.lastResize = body
		return 200, fmt.Sprintf(`{"data":%q}`, c.task(seg[1], "OK"))

	case method == "DELETE" && len(seg) == 4:
		vmid := atoiSafe(seg[3])
		if c.guests[vmid] == nil {
			return 500, `{"errors":{"vmid":"no such guest"}}`
		}
		if c.guests[vmid].Status != "stopped" {
			return 500, `{"errors":{"vmid":"guest is running"}}`
		}
		delete(c.guests, vmid)
		c.destroyed = append(c.destroyed, vmid)
		return 200, fmt.Sprintf(`{"data":%q}`, c.task(seg[1], "OK"))
	}
	return 404, `{"data":null}`
}

func (c *pmxCluster) resources() string {
	rows := make([]string, 0)
	for vmid, g := range c.guests {
		rows = append(rows, fmt.Sprintf(`{"vmid":%d,"node":%q,"type":%q,"status":%q}`, vmid, g.Node, g.Type, g.Status))
	}
	return `{"data":[` + strings.Join(rows, ",") + `]}`
}

func jsonObject(fields map[string]any) string {
	parts := make([]string, 0)
	for k, v := range fields {
		parts = append(parts, fmt.Sprintf(`%q:%q`, k, str(v)))
	}
	return `{"data":{` + strings.Join(parts, ",") + `}}`
}

// -----------------------------------------------------------------------------
// Scenarios
// -----------------------------------------------------------------------------

// pmxPendingVMID is the id the mock clones into a task that never finishes,
// exercising the module's pending-provision result.
const pmxPendingVMID = 700

func runProxmox(wasm []byte) {
	pmxTokenAuth(wasm)
	pmxTicketAuth(wasm)
	pmxDeployContainer(wasm)
	pmxDeployClone(wasm)
	pmxPlanChange(wasm)
	pmxTerminate(wasm)
	pmxPowerActions(wasm)
	pmxPendingClone(wasm)
	pmxConfigOptions(wasm)
}

// pmxArgs builds a module argument document for the mock cluster. auth selects
// the credential pair, vmid is the id HostCore replays after a deploy.
func pmxArgs(auth, vmid, extra string) string {
	creds := `"api_token_id":"root@pam!hostcore","api_token_secret":"7f3b-token"`
	if auth == "ticket" {
		creds = `"api_user":"root","api_password":"pve-secret"`
	}
	remote := ""
	if vmid != "" {
		remote = `"remote_id":"` + vmid + `",`
	}
	return `{
		"hostname":"pve.test",
		"port":"8006",
		` + creds + `,
		` + remote + `
		"service_id":"1024",
		"password":"secret123",
		"user":{"id":"77","email":"Alice@Example.com","first_name":"Alice","last_name":"Ng"},
		"service_config":[` + extra + `]
	}`
}

func pmxTokenAuth(wasm []byte) {
	fmt.Println("── connection test over an API token")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)

	assert("token connection test", call(plugin, "TestModuleConnection", pmxArgs("token", "", ""))["success"] == true, nil)
	assert("no ticket requested for a token login", c.tickets == 0, c.tickets)
	assert("missing credentials fail", call(plugin, "TestModuleConnection", `{"hostname":"pve.test"}`)["success"] == false, nil)
	assert("missing hostname fails", call(plugin, "TestModuleConnection", `{}`)["success"] == false, nil)
}

func pmxTicketAuth(wasm []byte) {
	fmt.Println("── connection test over a ticket login")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)

	res := call(plugin, "TestModuleConnection", pmxArgs("ticket", "", ""))
	assert("ticket connection test", res["success"] == true, res)
	assert("bare user name gained the pam realm", c.tickets == 1, c.tickets)

	bad := strings.Replace(pmxArgs("ticket", "", ""), "pve-secret", "wrong", 1)
	assert("wrong password fails", call(plugin, "TestModuleConnection", bad)["success"] == false, nil)
}

func pmxDeployContainer(wasm []byte) {
	fmt.Println("── deploy an LXC container onto the emptiest node")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)

	args := pmxArgs("token", "", strings.Join([]string{
		cfg("guest_type", "lxc"), cfg("ostemplate", "local:vztmpl/debian-12.tar.zst"),
		cfg("storage", "local"), cfg("disk", "10"), cfg("memory", "2048"), cfg("swap", "512"),
		cfg("cores", "2"), cfg("vlan", "40"), cfg("netrate", "50"),
	}, ","))
	res := call(plugin, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)

	data, _ := res["data"].(map[string]any)
	assert("VMID comes from cluster/nextid", toInt(data["server_id"]) == 105, data["server_id"])
	assert("node with the most free memory chosen", data["hostname"] == "pve2", data["hostname"])
	assert("guest type reported", data["guest_type"] == "lxc", data["guest_type"])
	assert("guest named for the service", data["name"] == "vps1024", data["name"])
	assert("no pending flag on a completed task", data["provision_pending"] == nil, data["provision_pending"])
	creds, _ := data["credentials"].(map[string]any)
	assert("credentials reported for a fresh container", creds["username"] == "root" && creds["password"] == "secret123", data["credentials"])
	assert("DHCP lease reported from the container's interfaces", data["ip"] == "198.51.100.24", data["ip"])

	assert("create sent the OS template", c.lastCreate["ostemplate"] == "local:vztmpl/debian-12.tar.zst", c.lastCreate["ostemplate"])
	assert("rootfs built from storage and disk", c.lastCreate["rootfs"] == "local:10", c.lastCreate["rootfs"])
	assert("memory forwarded", toInt(c.lastCreate["memory"]) == 2048, c.lastCreate["memory"])
	assert("swap forwarded", toInt(c.lastCreate["swap"]) == 512, c.lastCreate["swap"])
	assert("cores forwarded", toInt(c.lastCreate["cores"]) == 2, c.lastCreate["cores"])
	assert("container is unprivileged by default", str(c.lastCreate["unprivileged"]) == "1", c.lastCreate["unprivileged"])
	assert("root password forwarded", c.lastCreate["password"] == "secret123", c.lastCreate["password"])
	assert("guest starts after deploy", str(c.lastCreate["start"]) == "1", c.lastCreate["start"])
	assert("net0 carries bridge, vlan, rate and dhcp",
		c.lastCreate["net0"] == "name=eth0,bridge=vmbr0,firewall=0,rate=50,tag=40,ip=dhcp", c.lastCreate["net0"])

	fmt.Println("── static addressing and a VMID floor")
	static := pmxArgs("token", "", strings.Join([]string{
		cfg("guest_type", "lxc"), cfg("ostemplate", "local:vztmpl/debian-12.tar.zst"),
		cfg("memory", "1024"), cfg("start_vmid", "9000"),
		cfg("ip_address", "203.0.113.10/24"), cfg("gateway", "203.0.113.1"),
		cfg("nameserver", "9.9.9.9"), cfg("node", "pve1"),
	}, ","))
	res = call(plugin, "DeployService", static)
	assert("deploy succeeds", res["success"] == true, res)
	data, _ = res["data"].(map[string]any)
	assert("VMID skips the taken floor", toInt(data["server_id"]) == 9001, data["server_id"])
	assert("configured node honoured", data["hostname"] == "pve1", data["hostname"])
	assert("static address reported without its prefix", data["ip"] == "203.0.113.10", data["ip"])
	assert("net0 carries the static address",
		c.lastCreate["net0"] == "name=eth0,bridge=vmbr0,firewall=0,ip=203.0.113.10/24,gw=203.0.113.1", c.lastCreate["net0"])
	assert("nameservers forwarded", c.lastCreate["nameserver"] == "9.9.9.9", c.lastCreate["nameserver"])

	assert("deploy without a guest type fails", call(plugin, "DeployService", pmxArgs("token", "", cfg("memory", "1024")))["success"] == false, nil)
	assert("container without an OS template fails",
		call(plugin, "DeployService", pmxArgs("token", "", cfg("guest_type", "lxc")))["success"] == false, nil)
}

func pmxDeployClone(wasm []byte) {
	fmt.Println("── deploy a QEMU guest by cloning a template")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)

	args := pmxArgs("token", "", strings.Join([]string{
		cfg("guest_type", "qemu"), cfg("template_vmid", "9000"), cfg("memory", "4096"),
		cfg("cores", "4"), cfg("storage", "local-lvm"), cfg("ssh_keys", "ssh-ed25519 AAAA"),
	}, ","))
	res := call(plugin, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)

	data, _ := res["data"].(map[string]any)
	vmid := toInt(data["server_id"])
	assert("clone sent the new VMID", toInt(c.lastClone["newid"]) == vmid, c.lastClone["newid"])
	assert("clone is a full copy", str(c.lastClone["full"]) == "1", c.lastClone["full"])
	assert("clone targets the chosen node", c.lastClone["target"] == "pve2", c.lastClone["target"])
	assert("clone names the guest", c.lastClone["name"] == "vps1024", c.lastClone["name"])

	assert("plan resources applied after the clone", toInt(c.lastConfig["memory"]) == 4096, c.lastConfig["memory"])
	assert("cloud-init password applied", c.lastConfig["cipassword"] == "secret123", c.lastConfig["cipassword"])
	assert("cloud-init addressing applied", c.lastConfig["ipconfig0"] == "ip=dhcp", c.lastConfig["ipconfig0"])
	assert("ssh keys applied", c.lastConfig["sshkeys"] == "ssh-ed25519 AAAA", c.lastConfig["sshkeys"])
	assert("guest started after the clone", len(c.started) == 1 && c.started[0] == vmid, c.started)
	creds, _ := data["credentials"].(map[string]any)
	assert("credentials reported for a cloned VM", creds["password"] == "secret123", data["credentials"])
	assert("no address while the guest agent is down", data["ip"] == nil, data["ip"])

	fmt.Println("── the guest agent supplies the address once it is up")
	c2 := newPmx()
	c2.agentUp = true
	plugin2 := loadPlugin(wasm, c2.route)
	res = call(plugin2, "DeployService", args)
	assert("deploy succeeds", res["success"] == true, res)
	data, _ = res["data"].(map[string]any)
	assert("IPv4 taken from the agent, skipping loopback and IPv6", data["ip"] == "198.51.100.77", data["ip"])

	assert("cloning a missing template fails",
		call(plugin, "DeployService", pmxArgs("token", "", cfg("guest_type", "qemu")+","+cfg("template_vmid", "4242")+","+cfg("memory", "1024")))["success"] == false, nil)
	assert("cloning across guest types fails",
		call(plugin, "DeployService", pmxArgs("token", "", cfg("guest_type", "lxc")+","+cfg("template_vmid", "9000")+","+cfg("memory", "1024")))["success"] == false, nil)
}

func pmxPlanChange(wasm []byte) {
	fmt.Println("── suspend / unsuspend / plan change / password")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)
	c.guests[121] = &pmxGuest{Node: "pve1", Type: "qemu", Status: "running", Config: map[string]any{
		"net0": "virtio=1A:2B:3C:4D:5E:6F,bridge=vmbr0",
	}}

	lc := pmxArgs("token", "121", strings.Join([]string{
		cfg("guest_type", "qemu"), cfg("memory", "8192"), cfg("cores", "6"), cfg("disk", "40"),
	}, ","))

	assert("suspend", call(plugin, "SuspendService", lc)["success"] == true, nil)
	assert("guest stopped", c.guests[121].Status == "stopped", c.guests[121].Status)
	assert("suspending a stopped guest is a no-op", call(plugin, "SuspendService", lc)["success"] == true, nil)
	assert("stop issued once", len(c.stopped) == 1, c.stopped)

	assert("unsuspend", call(plugin, "UnsuspendService", lc)["success"] == true, nil)
	assert("guest running", c.guests[121].Status == "running", c.guests[121].Status)
	assert("unsuspending a running guest is a no-op", call(plugin, "UnsuspendService", lc)["success"] == true, nil)
	assert("start issued once", len(c.started) == 1, c.started)

	assert("plan change", call(plugin, "UpdateService", lc)["success"] == true, nil)
	assert("memory raised", toInt(c.lastConfig["memory"]) == 8192, c.lastConfig["memory"])
	assert("cores raised", toInt(c.lastConfig["cores"]) == 6, c.lastConfig["cores"])
	assert("hardware address preserved across the rewrite",
		c.lastConfig["net0"] == "virtio=1A:2B:3C:4D:5E:6F,bridge=vmbr0,firewall=0", c.lastConfig["net0"])
	assert("root disk resized", c.lastResize["disk"] == "scsi0" && c.lastResize["size"] == "40G", c.lastResize)

	pw := call(plugin, "ChangePassword", lc)
	assert("change password", pw["success"] == true, pw)
	pwData, _ := pw["data"].(map[string]any)
	assert("new password returned", pwData["password"] == "secret123", pwData)
	assert("cloud-init password written", c.lastConfig["cipassword"] == "secret123", c.lastConfig["cipassword"])

	c.guests[122] = &pmxGuest{Node: "pve1", Type: "lxc", Status: "running", Config: map[string]any{}}
	ct := pmxArgs("token", "122", cfg("guest_type", "lxc")+","+cfg("memory", "2048"))
	assert("container plan change uses the PUT endpoint", call(plugin, "UpdateService", ct)["success"] == true, nil)
	assert("container password change is refused", call(plugin, "ChangePassword", ct)["success"] == false, nil)

	assert("lifecycle call without a VMID fails", call(plugin, "SuspendService", pmxArgs("token", "", ""))["success"] == false, nil)
	assert("lifecycle call on an unknown VMID fails", call(plugin, "SuspendService", pmxArgs("token", "6543", ""))["success"] == false, nil)
}

func pmxTerminate(wasm []byte) {
	fmt.Println("── terminate")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)
	c.guests[131] = &pmxGuest{Node: "pve1", Type: "qemu", Status: "running", Config: map[string]any{}}

	lc := pmxArgs("token", "131", "")
	assert("terminate", call(plugin, "TerminateService", lc)["success"] == true, nil)
	assert("guest stopped before destroy", len(c.stopped) == 1 && c.stopped[0] == 131, c.stopped)
	assert("guest destroyed", c.guests[131] == nil, c.guests[131])
	assert("terminate is idempotent (VMID gone → success)", call(plugin, "TerminateService", lc)["success"] == true, nil)
}

func pmxPendingClone(wasm []byte) {
	fmt.Println("── a clone still running at the end of the poll budget binds the VMID")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)

	args := pmxArgs("token", "", strings.Join([]string{
		cfg("guest_type", "qemu"), cfg("template_vmid", "9000"), cfg("memory", "2048"),
		cfg("start_vmid", fmt.Sprintf("%d", pmxPendingVMID)),
	}, ","))
	res := call(plugin, "DeployService", args)
	assert("deploy reports success", res["success"] == true, res)

	data, _ := res["data"].(map[string]any)
	assert("VMID bound to the service", toInt(data["server_id"]) == pmxPendingVMID, data["server_id"])
	assert("provision flagged as pending", data["provision_pending"] == true, data["provision_pending"])
	assert("task UPID reported", strings.HasPrefix(str(data["task"]), "UPID:"), data["task"])
	assert("no credentials while the clone runs", data["credentials"] == nil, data["credentials"])
	assert("no config applied while the clone runs", c.lastConfig == nil, c.lastConfig)
	assert("guest not started while the clone runs", len(c.started) == 0, c.started)

	fmt.Println("── a failed task fails the deploy")
	c2 := newPmx()
	c2.guests[9000].Node = "pve1"
	plugin2 := loadPlugin(wasm, func(method, rawURL string, body map[string]any) (int, string) {
		status, out := c2.route(method, rawURL, body)
		if method == "GET" && strings.Contains(rawURL, "/tasks/") {
			return 200, `{"data":{"status":"stopped","exitstatus":"clone failed: no space left on device"}}`
		}
		return status, out
	})
	failed := call(plugin2, "DeployService", pmxArgs("token", "", strings.Join([]string{
		cfg("guest_type", "qemu"), cfg("template_vmid", "9000"), cfg("memory", "2048"),
	}, ",")))
	assert("deploy fails", failed["success"] == false, failed)
	assert("failure names the exit status", strings.Contains(str(failed["error"]), "no space left on device"), failed["error"])
}

func pmxPowerActions(wasm []byte) {
	fmt.Println("── customer power actions")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)
	c.guests[141] = &pmxGuest{Node: "pve1", Type: "qemu", Status: "running", Config: map[string]any{}}

	lc := pmxArgs("token", "141", "")
	withAction := func(action string) string {
		return strings.Replace(lc, `"service_id":"1024"`, `"service_id":"1024","action":"`+action+`"`, 1)
	}

	res := call(plugin, "ServiceAction", withAction("restart"))
	assert("restart", res["success"] == true, res)
	assert("reboot issued", len(c.rebooted) == 1 && c.rebooted[0] == 141, c.rebooted)
	data, _ := res["data"].(map[string]any)
	assert("status reported back", data["status"] == "running", data)
	assert("action echoed", data["action"] == "restart", data)

	res = call(plugin, "ServiceAction", withAction("shutdown"))
	assert("shutdown", res["success"] == true, res)
	assert("guest stopped", c.guests[141].Status == "stopped", c.guests[141].Status)
	data, _ = res["data"].(map[string]any)
	assert("stopped status reported back", data["status"] == "stopped", data)

	assert("restarting a stopped guest is refused", call(plugin, "ServiceAction", withAction("restart"))["success"] == false, nil)
	assert("shutting down a stopped guest is refused", call(plugin, "ServiceAction", withAction("shutdown"))["success"] == false, nil)

	res = call(plugin, "ServiceAction", withAction("start"))
	assert("start", res["success"] == true, res)
	assert("guest running", c.guests[141].Status == "running", c.guests[141].Status)

	res = call(plugin, "ServiceAction", withAction("stop"))
	assert("stop", res["success"] == true, res)
	assert("guest stopped", c.guests[141].Status == "stopped", c.guests[141].Status)

	assert("an unknown action is refused", call(plugin, "ServiceAction", withAction("selfdestruct"))["success"] == false, nil)
	assert("an action without a VMID is refused", call(plugin, "ServiceAction", `{"hostname":"pve.test","api_token_id":"a@pam!b","api_token_secret":"c","action":"start"}`)["success"] == false, nil)
	assert("an action on an unknown VMID is refused", call(plugin, "ServiceAction", strings.Replace(withAction("start"), `"remote_id":"141"`, `"remote_id":"9911"`, 1))["success"] == false, nil)
}

func pmxConfigOptions(wasm []byte) {
	fmt.Println("── GetServiceConfigOptions returns valid schema")
	c := newPmx()
	plugin := loadPlugin(wasm, c.route)
	res := call(plugin, "GetServiceConfigOptions", `{}`)
	assert("config options succeeds", res["success"] == true, res)

	data, _ := res["data"].([]any)
	required := map[string]bool{}
	keys := map[string]bool{}
	for _, f := range data {
		m, _ := f.(map[string]any)
		keys[str(m["key"])] = true
		if m["required"] == true {
			required[str(m["key"])] = true
		}
	}
	assert("guest_type & memory required", required["guest_type"] && required["memory"], required)
	assert("template, storage and networking exposed",
		keys["template_vmid"] && keys["ostemplate"] && keys["storage"] && keys["bridge"] && keys["ip_address"], keys)
}
