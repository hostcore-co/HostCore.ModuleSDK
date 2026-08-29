// HostCore Service Module — Proxmox VE
//
// This is a HostCore service module for the Proxmox Virtual Environment
// hypervisor. It provisions and manages QEMU virtual machines and LXC
// containers through the Proxmox VE API v2
// (https://pve.proxmox.com/pve-docs/api-viewer/), the surface the Proxmox VE
// for WHMCS module drives. Requests carry JSON bodies, which pvedaemon decodes
// from PVE 7 on, so that is the floor this module targets.
//
// A guest is addressed by its cluster-wide VMID. DeployService allocates one,
// returns it as "server_id", and HostCore replays it on later lifecycle calls.
// The node a guest lives on is resolved from /cluster/resources on every call
// rather than trusted from the deploy, so a guest that has been migrated is
// still reachable.
//
// Proxmox answers every mutating call with a UPID naming a background task.
// The module polls that task so a lifecycle call only reports success once the
// hypervisor has finished the work. Engine executions are bounded, so the poll
// budget is too: a clone still running when it runs out is reported as a
// successful deploy carrying "provision_pending" and the task's UPID, which
// binds the service to the VMID Proxmox already allocated.
//
// A module script may import only the HostCore.ModuleSDK. All outbound HTTP
// flows through the SDK's host functions.
//
// # Input contract
//
//	{
//	  "hostname":         "pve.example.com", // Proxmox host (no scheme)
//	  "port":             "8006",            // API port, defaults to 8006
//	  "api_token_id":     "root@pam!hostcore",
//	  "api_token_secret": "xxxxxxxx-xxxx-...",
//	  "api_user":         "root@pam",        // ticket login, used when no token is set
//	  "api_password":     "secret",
//	  "remote_id":        "121",             // VMID (lifecycle calls after deploy)
//	  "service_id":       "1024",            // store service id, names the guest
//	  "password":         "root-secret",     // guest root password (deploy / change-password)
//	  "user":             { "id", "email", "first_name", "last_name" },
//	  "service_config":   [ { "key": "...", "value": "..." }, ... ]
//	}
package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// main is required so the module can be compiled both with TinyGo and with the
// standard Go wasip1 tool-chain (go build -buildmode=c-shared). It is never
// executed for exported reactor calls.
func main() {}

const (
	// taskPollAttempts and taskPollDelayMS bound how long a lifecycle call
	// waits for a Proxmox background task. The product must stay comfortably
	// inside the engine's execution timeout, which the requests around the
	// poll also draw on.
	taskPollAttempts = 14
	taskPollDelayMS  = 1000

	// maxVMIDProbe caps how far past a configured start VMID the module looks
	// for a free id before giving up.
	maxVMIDProbe = 512
)

// -----------------------------------------------------------------------------
// Exported lifecycle functions
// -----------------------------------------------------------------------------

// TestModuleConnection verifies that Proxmox is reachable and the credentials
// authenticate by reading the cluster version.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	p, msg := newPVE(sdk.GetArgs())
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	resp := p.get("version")
	if resp.Status == 401 || resp.Status == 403 {
		return sdk.Fail("Authentication failed — check the Proxmox credentials and their permissions.", resp.Body)
	}
	if !resp.OK {
		return sdk.Fail("Connection failed with status "+sdk.Itoa(resp.Status)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Guest Type","type":"string","key":"guest_type","description":"qemu for a virtual machine, lxc for a container.","regex":"^(qemu|lxc)$","required":true},
		{"order":2,"label":"Node","type":"string","key":"node","description":"Cluster node to deploy on. The online node with the most free memory is chosen when blank.","regex":"","required":false},
		{"order":3,"label":"Template VMID","type":"number","key":"template_vmid","description":"VMID of a template to clone. Left blank, the guest is built from scratch.","regex":"^[0-9]+$","required":false},
		{"order":4,"label":"Full Clone","type":"bool","key":"full_clone","description":"Copy the template's disks instead of linking them. Defaults to on; a linked clone stays on the template's node.","regex":"","required":false},
		{"order":5,"label":"OS Template","type":"string","key":"ostemplate","description":"LXC template volume, e.g. local:vztmpl/debian-12-standard_12.7-1_amd64.tar.zst. Required for lxc without a template VMID.","regex":"","required":false},
		{"order":6,"label":"ISO Image","type":"string","key":"iso","description":"QEMU installation ISO volume, e.g. local:iso/debian-12.iso.","regex":"","required":false},
		{"order":7,"label":"Storage","type":"string","key":"storage","description":"Storage the guest disk is created on, e.g. local-lvm.","regex":"","required":false},
		{"order":8,"label":"Disk (GB)","type":"number","key":"disk","description":"Root disk size. Proxmox can grow a disk but never shrink one.","regex":"^[0-9]+$","required":false},
		{"order":9,"label":"Disk Bus","type":"string","key":"disk_bus","description":"QEMU disk bus (scsi, virtio, sata, ide). Defaults to scsi.","regex":"^(scsi|virtio|sata|ide)$","required":false},
		{"order":10,"label":"Memory (MB)","type":"number","key":"memory","description":"Guest memory.","regex":"^[0-9]+$","required":true},
		{"order":11,"label":"Swap (MB)","type":"number","key":"swap","description":"LXC swap size.","regex":"^[0-9]+$","required":false},
		{"order":12,"label":"CPU Cores","type":"number","key":"cores","description":"Cores per socket.","regex":"^[0-9]+$","required":false},
		{"order":13,"label":"CPU Sockets","type":"number","key":"sockets","description":"QEMU sockets. Defaults to 1.","regex":"^[0-9]+$","required":false},
		{"order":14,"label":"CPU Limit","type":"number","key":"cpulimit","description":"Ceiling on CPU time, in whole cores. 0 is unlimited.","regex":"^[0-9]+$","required":false},
		{"order":15,"label":"CPU Units","type":"number","key":"cpuunits","description":"Relative CPU weight against other guests.","regex":"^[0-9]+$","required":false},
		{"order":16,"label":"OS Type","type":"string","key":"ostype","description":"Proxmox OS type hint, e.g. l26, win11.","regex":"","required":false},
		{"order":17,"label":"Bridge","type":"string","key":"bridge","description":"Network bridge to attach to. Defaults to vmbr0.","regex":"^vmbr[0-9]+$","required":false},
		{"order":18,"label":"VLAN Tag","type":"number","key":"vlan","description":"VLAN tag for the guest interface.","regex":"^[0-9]+$","required":false},
		{"order":19,"label":"Network Rate (MB/s)","type":"number","key":"netrate","description":"Rate limit on the guest interface.","regex":"^[0-9]+$","required":false},
		{"order":20,"label":"Network Model","type":"string","key":"net_model","description":"QEMU NIC model (virtio, e1000, rtl8139). Defaults to virtio.","regex":"","required":false},
		{"order":21,"label":"Firewall","type":"bool","key":"firewall","description":"Enable the Proxmox firewall on the guest interface.","regex":"","required":false},
		{"order":22,"label":"IP Address","type":"string","key":"ip_address","description":"Guest address in CIDR form, e.g. 203.0.113.10/24. Defaults to dhcp.","regex":"","required":false},
		{"order":23,"label":"Gateway","type":"string","key":"gateway","description":"Default gateway, used with a static IP address.","regex":"","required":false},
		{"order":24,"label":"Nameservers","type":"string","key":"nameserver","description":"Space-separated resolvers. The node's own are used when blank.","regex":"","required":false},
		{"order":25,"label":"SSH Public Keys","type":"string","key":"ssh_keys","description":"Public keys authorised for the guest's root user.","regex":"","required":false},
		{"order":26,"label":"Resource Pool","type":"string","key":"pool","description":"Proxmox pool to file the guest under.","regex":"","required":false},
		{"order":27,"label":"Unprivileged","type":"bool","key":"unprivileged","description":"Create the LXC container unprivileged. Defaults to on.","regex":"","required":false},
		{"order":28,"label":"Start On Boot","type":"bool","key":"onboot","description":"Start the guest when its node boots.","regex":"","required":false},
		{"order":29,"label":"Start After Deploy","type":"bool","key":"start_on_deploy","description":"Power the guest on once it is provisioned. Defaults to on.","regex":"","required":false},
		{"order":30,"label":"Starting VMID","type":"number","key":"start_vmid","description":"Lowest VMID this plan may allocate. Proxmox picks the cluster's next id when blank.","regex":"^[0-9]+$","required":false},
		{"order":31,"label":"Hostname","type":"string","key":"hostname","description":"Guest name. Derived from the service id when blank.","regex":"","required":false}
	]`))
}

// DeployService allocates a VMID and either clones the configured template or
// builds a guest from scratch, then applies the plan's resources.
//
//go:wasmexport DeployService
func DeployService() int32 {
	args := sdk.GetArgs()
	p, msg := newPVE(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	gtype := sdk.ToLower(cfgStr(args, "guest_type"))
	if gtype != "qemu" && gtype != "lxc" {
		return sdk.Fail("guest_type must be qemu or lxc.", nil)
	}

	node, msg := p.pickNode(cfgStr(args, "node"))
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	vmid, msg := p.nextVMID(sdk.Atoi(cfgStr(args, "start_vmid")))
	if msg != "" {
		return sdk.Fail(msg, nil)
	}

	name := guestName(args)
	password := guestPassword(args)

	if template := cfgStr(args, "template_vmid"); template != "" {
		return p.deployClone(args, template, gtype, node, vmid, name, password)
	}
	return p.deployFresh(args, gtype, node, vmid, name, password)
}

// SuspendService stops the guest bound to this service. A guest that is already
// stopped is left alone.
//
//go:wasmexport SuspendService
func SuspendService() int32 {
	args := sdk.GetArgs()
	p, g, vmid, msg := resolve(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !g.found() {
		return sdk.Fail("No Proxmox guest with VMID "+vmid+" exists on the cluster.", nil)
	}
	if g.status == "stopped" {
		return sdk.Success(nil)
	}
	if msg := p.power(g, vmid, "stop"); msg != "" {
		return sdk.Fail("Failed to suspend the guest"+withReason(msg)+".", nil)
	}
	return sdk.Success(nil)
}

// UnsuspendService starts the guest bound to this service. A guest that is
// already running is left alone.
//
//go:wasmexport UnsuspendService
func UnsuspendService() int32 {
	args := sdk.GetArgs()
	p, g, vmid, msg := resolve(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !g.found() {
		return sdk.Fail("No Proxmox guest with VMID "+vmid+" exists on the cluster.", nil)
	}
	if g.status == "running" {
		return sdk.Success(nil)
	}
	if msg := p.power(g, vmid, "start"); msg != "" {
		return sdk.Fail("Failed to unsuspend the guest"+withReason(msg)+".", nil)
	}
	return sdk.Success(nil)
}

// TerminateService stops the guest and destroys it along with its disks.
// A VMID no longer on the cluster is treated as already terminated.
//
//go:wasmexport TerminateService
func TerminateService() int32 {
	args := sdk.GetArgs()
	p, g, vmid, msg := resolve(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !g.found() {
		return sdk.Success(nil)
	}

	if g.status != "stopped" {
		if msg := p.power(g, vmid, "stop"); msg != "" {
			return sdk.Fail("Failed to stop the guest before terminating it"+withReason(msg)+".", nil)
		}
	}

	resp := p.delete(g.path(vmid) + "?purge=1")
	if resp.Status == 404 {
		return sdk.Success(nil)
	}
	if !resp.OK {
		return sdk.Fail("Failed to terminate the guest"+withReason(pveError(resp))+".", resp.Body)
	}
	if msg := p.settle(g.node, upidOf(resp)); msg != "" {
		return sdk.Fail("Failed to terminate the guest"+withReason(msg)+".", resp.Body)
	}
	return sdk.Success(nil)
}

// UpdateService applies a changed plan to the running guest: the resource
// limits and networking through the config endpoint, then the root disk size
// through a resize. Proxmox refuses a disk smaller than the current one.
//
//go:wasmexport UpdateService
func UpdateService() int32 {
	args := sdk.GetArgs()
	p, g, vmid, msg := resolve(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !g.found() {
		return sdk.Fail("No Proxmox guest with VMID "+vmid+" exists on the cluster.", nil)
	}

	current, msg := p.config(g, vmid)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if msg := p.applyConfig(g, vmid, resources(args, g.gtype, macOf(current, g.gtype))); msg != "" {
		return sdk.Fail("Failed to apply the new resources"+withReason(msg)+".", nil)
	}

	disk := cfgStr(args, "disk")
	if disk == "" {
		return sdk.Success(sdk.NewJSON().Set("server_id", sdk.Atoi(vmid)).Build())
	}

	resp := p.put(g.path(vmid)+"/resize", sdk.NewJSON().
		Set("disk", rootDisk(args, g.gtype)).
		Set("size", disk+"G").
		Build())
	if !resp.OK {
		return sdk.Fail("Failed to resize the root disk"+withReason(pveError(resp))+".", resp.Body)
	}
	if msg := p.settle(g.node, upidOf(resp)); msg != "" {
		return sdk.Fail("Failed to resize the root disk"+withReason(msg)+".", resp.Body)
	}
	return sdk.Success(sdk.NewJSON().Set("server_id", sdk.Atoi(vmid)).Build())
}

// ChangePassword sets the cloud-init password of a QEMU guest. It takes effect
// on the guest's next boot, when cloud-init reads the drive again. Proxmox
// exposes no password endpoint for a running LXC container, so the call is
// refused for one rather than reporting a change that never happened.
//
//go:wasmexport ChangePassword
func ChangePassword() int32 {
	args := sdk.GetArgs()
	p, g, vmid, msg := resolve(args)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !g.found() {
		return sdk.Fail("No Proxmox guest with VMID "+vmid+" exists on the cluster.", nil)
	}
	if g.gtype == "lxc" {
		return sdk.Fail("Proxmox has no API for changing an existing container's password; reset it from the container's console.", nil)
	}

	password := guestPassword(args)
	if msg := p.applyConfig(g, vmid, sdk.NewJSON().Set("cipassword", password)); msg != "" {
		return sdk.Fail("Failed to set the cloud-init password"+withReason(msg)+".", nil)
	}
	return sdk.Success(sdk.NewJSON().Set("password", password).Build())
}

// -----------------------------------------------------------------------------
// Deployment
// -----------------------------------------------------------------------------

// deployClone provisions the guest by cloning a template. A linked clone cannot
// leave the template's node, so one is deployed where the template lives.
func (p pve) deployClone(args sdk.Args, template, gtype, node string, vmid int, name, password string) int32 {
	source, msg := p.findGuest(template)
	if msg != "" {
		return sdk.Fail(msg, nil)
	}
	if !source.found() {
		return sdk.Fail("Template VMID "+template+" does not exist on the cluster.", nil)
	}
	if source.gtype != gtype {
		return sdk.Fail("Template VMID "+template+" is a "+source.gtype+" guest but guest_type is "+gtype+".", nil)
	}

	full := cfgBool(args, "full_clone", true)
	body := sdk.NewJSON().Set("newid", vmid)
	if gtype == "lxc" {
		body.Set("hostname", name)
	} else {
		body.Set("name", name)
	}
	if full {
		body.Set("full", 1).Set("target", node)
	} else {
		node = source.node
	}
	setStr(body, args, "storage", "storage")
	setStr(body, args, "pool", "pool")

	resp := p.post("nodes/"+source.node+"/"+gtype+"/"+template+"/clone", body.Build())
	if !resp.OK {
		return sdk.Fail("Failed to clone template "+template+withReason(pveError(resp))+".", resp.Body)
	}

	result := deployResult(vmid, node, gtype, name)
	done, failure := p.waitTask(source.node, upidOf(resp))
	if failure != "" {
		return sdk.Fail("The clone of template "+template+" failed"+withReason(failure)+".", resp.Body)
	}
	if !done {
		// The VMID is allocated and Proxmox is still copying disks. Reporting
		// success binds the service to the guest; a failure here would leave it
		// running with nothing on this side addressing it.
		return sdk.Success(result.Set("provision_pending", true).Set("task", upidOf(resp)).Build())
	}

	g := guest{node: node, gtype: gtype}
	tuning := resources(args, gtype, "")
	if gtype == "qemu" {
		tuning.Set("cipassword", password).Set("ciuser", "root")
		setStr(tuning, args, "ssh_keys", "sshkeys")
	}
	if msg := p.applyConfig(g, sdk.Itoa(vmid), tuning); msg != "" {
		return sdk.Fail("The guest was cloned but its configuration could not be applied"+withReason(msg)+".", nil)
	}

	if cfgBool(args, "start_on_deploy", true) {
		if msg := p.power(g, sdk.Itoa(vmid), "start"); msg != "" {
			return sdk.Fail("The guest was cloned but would not start"+withReason(msg)+".", nil)
		}
	}
	return sdk.Success(result.Build())
}

// deployFresh provisions the guest from an OS template (LXC) or an empty disk
// and an optional ISO (QEMU).
func (p pve) deployFresh(args sdk.Args, gtype, node string, vmid int, name, password string) int32 {
	var body *sdk.JSON
	if gtype == "lxc" {
		ostemplate := cfgStr(args, "ostemplate")
		if ostemplate == "" {
			return sdk.Fail("ostemplate is required to create a container without a template VMID.", nil)
		}
		body = createLXC(args, vmid, name, ostemplate, password)
	} else {
		body = createQEMU(args, vmid, name, password)
	}
	if cfgBool(args, "start_on_deploy", true) {
		body.Set("start", 1)
	}

	resp := p.post("nodes/"+node+"/"+gtype, body.Build())
	if !resp.OK {
		return sdk.Fail("Failed to create the guest"+withReason(pveError(resp))+".", resp.Body)
	}

	result := deployResult(vmid, node, gtype, name)
	done, failure := p.waitTask(node, upidOf(resp))
	if failure != "" {
		return sdk.Fail("The guest creation failed"+withReason(failure)+".", resp.Body)
	}
	if !done {
		return sdk.Success(result.Set("provision_pending", true).Set("task", upidOf(resp)).Build())
	}
	return sdk.Success(result.Build())
}

// createQEMU builds the payload for POST /nodes/{node}/qemu.
func createQEMU(args sdk.Args, vmid int, name, password string) *sdk.JSON {
	body := resources(args, "qemu", "").
		Set("vmid", vmid).
		Set("name", name).
		Set("scsihw", "virtio-scsi-single").
		Set("cipassword", password).
		Set("ciuser", "root")

	if disk := cfgStr(args, "disk"); disk != "" {
		body.Set(rootDisk(args, "qemu"), storagePool(args)+":"+disk)
	}
	if iso := cfgStr(args, "iso"); iso != "" {
		body.Set("ide2", iso+",media=cdrom")
	}
	setStr(body, args, "ssh_keys", "sshkeys")
	setStr(body, args, "pool", "pool")
	return body
}

// createLXC builds the payload for POST /nodes/{node}/lxc.
func createLXC(args sdk.Args, vmid int, name, ostemplate, password string) *sdk.JSON {
	body := resources(args, "lxc", "").
		Set("vmid", vmid).
		Set("hostname", name).
		Set("ostemplate", ostemplate).
		Set("password", password).
		Set("unprivileged", boolParam(cfgBool(args, "unprivileged", true)))

	if disk := cfgStr(args, "disk"); disk != "" {
		body.Set("rootfs", storagePool(args)+":"+disk)
	}
	setStr(body, args, "ssh_keys", "ssh-public-keys")
	setStr(body, args, "pool", "pool")
	return body
}

// resources builds the fields shared by guest creation and a later plan change:
// the CPU, memory and network settings that a Proxmox config endpoint accepts
// unchanged for the life of the guest. A non-empty mac pins the interface to
// the address the guest already answers on.
func resources(args sdk.Args, gtype, mac string) *sdk.JSON {
	body := sdk.NewJSON()

	setInt(body, args, "memory", "memory")
	setInt(body, args, "cores", "cores")
	setInt(body, args, "cpulimit", "cpulimit")
	setInt(body, args, "cpuunits", "cpuunits")
	setStr(body, args, "nameserver", "nameserver")
	body.Set("onboot", boolParam(cfgBool(args, "onboot", false)))
	body.Set("net0", netDevice(args, gtype, mac))

	if gtype == "lxc" {
		setInt(body, args, "swap", "swap")
		return body
	}

	setStr(body, args, "ostype", "ostype")
	if sockets := cfgStr(args, "sockets"); sockets != "" {
		body.Set("sockets", sdk.Atoi(sockets))
	} else {
		body.Set("sockets", 1)
	}
	body.Set("ipconfig0", addressing(args))
	return body
}

// netDevice renders the net0 device string. An LXC interface carries its
// addressing inline; a QEMU one takes it from cloud-init's ipconfig0. Proxmox
// mints a fresh hardware address whenever one is left out, so rewriting the
// device on an existing guest must carry its current mac back in.
func netDevice(args sdk.Args, gtype, mac string) string {
	bridge := cfgStr(args, "bridge")
	if bridge == "" {
		bridge = "vmbr0"
	}

	dev := ""
	if gtype == "lxc" {
		dev = "name=eth0,bridge=" + bridge
		if mac != "" {
			dev += ",hwaddr=" + mac
		}
	} else {
		model := cfgStr(args, "net_model")
		if model == "" {
			model = "virtio"
		}
		dev = model
		if mac != "" {
			dev += "=" + mac
		}
		dev += ",bridge=" + bridge
	}

	dev += ",firewall=" + boolParam(cfgBool(args, "firewall", false))
	if rate := cfgStr(args, "netrate"); rate != "" {
		dev += ",rate=" + rate
	}
	if vlan := cfgStr(args, "vlan"); vlan != "" {
		dev += ",tag=" + vlan
	}
	if gtype == "lxc" {
		dev += "," + addressing(args)
	}
	return dev
}

// addressing renders the ip/gw pair Proxmox takes for both an LXC interface and
// a QEMU cloud-init ipconfig entry.
func addressing(args sdk.Args) string {
	addr := cfgStr(args, "ip_address")
	if addr == "" || sdk.EqualFold(addr, "dhcp") {
		return "ip=dhcp"
	}
	out := "ip=" + addr
	if gw := cfgStr(args, "gateway"); gw != "" {
		out += ",gw=" + gw
	}
	return out
}

// deployResult is the identity HostCore persists for the guest. The node is
// reported as "hostname", which the platform stores as the service's remote
// server.
func deployResult(vmid int, node, gtype, name string) *sdk.JSON {
	return sdk.NewJSON().
		Set("server_id", vmid).
		Set("hostname", node).
		Set("guest_type", gtype).
		Set("name", name)
}

// -----------------------------------------------------------------------------
// Proxmox client
// -----------------------------------------------------------------------------

// pve is a thin wrapper over the Proxmox VE API v2. It centralises the base URL
// and whichever authorisation the store configured.
type pve struct {
	base string
	hdrs map[string]string
}

// newPVE builds a client for the Proxmox host named in args. API-token
// credentials are used when both halves are present; otherwise the module logs
// in for a ticket and carries the auth cookie and CSRF token on every later
// request.
func newPVE(args sdk.Args) (pve, string) {
	host := sdk.TrimSpace(args.Get("hostname").String())
	if host == "" {
		return pve{}, "hostname is required."
	}
	port := sdk.TrimSpace(args.Get("port").String())
	if port == "" {
		port = "8006"
	}

	p := pve{
		base: "https://" + host + ":" + port + "/api2/json/",
		hdrs: map[string]string{
			"Accept":       "application/json",
			"Content-Type": "application/json",
		},
	}

	tokenID := sdk.TrimSpace(args.Get("api_token_id").String())
	tokenSecret := sdk.TrimSpace(args.Get("api_token_secret").String())
	if tokenID != "" && tokenSecret != "" {
		p.hdrs["Authorization"] = "PVEAPIToken=" + tokenID + "=" + tokenSecret
		return p, ""
	}

	user := withRealm(sdk.TrimSpace(args.Get("api_user").String()))
	password := args.Get("api_password").String()
	if user == "" || password == "" {
		return pve{}, "Configure either an API token (api_token_id and api_token_secret) or a login (api_user and api_password)."
	}

	resp := sdk.HTTPPost(p.base+"access/ticket", p.hdrs, sdk.NewJSON().
		Set("username", user).
		Set("password", password).
		Build())
	if resp.Status == 401 || resp.Status == 403 {
		return pve{}, "Authentication failed — check the Proxmox user name, realm and password."
	}
	if !resp.OK {
		return pve{}, "Proxmox login failed with status " + sdk.Itoa(resp.Status) + "."
	}

	data := sdk.GetBytes(resp.Body, "data")
	ticket := data.Get("ticket").String()
	if ticket == "" {
		return pve{}, "Proxmox accepted the login but returned no ticket."
	}
	p.hdrs["Cookie"] = "PVEAuthCookie=" + ticket
	p.hdrs["CSRFPreventionToken"] = data.Get("CSRFPreventionToken").String()
	return p, ""
}

func (p pve) get(endpoint string) sdk.HTTPResponse    { return sdk.HTTPGet(p.base+endpoint, p.hdrs) }
func (p pve) delete(endpoint string) sdk.HTTPResponse { return sdk.HTTPDelete(p.base+endpoint, p.hdrs) }

func (p pve) post(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPost(p.base+endpoint, p.hdrs, body)
}

func (p pve) put(endpoint string, body []byte) sdk.HTTPResponse {
	return sdk.HTTPPut(p.base+endpoint, p.hdrs, body)
}

// guest is where a VMID currently lives on the cluster and what state it is in.
type guest struct {
	node   string
	gtype  string
	status string
}

func (g guest) found() bool { return g.node != "" }

// path is the API prefix addressing this guest, e.g. nodes/pve1/qemu/121.
func (g guest) path(vmid string) string {
	return "nodes/" + g.node + "/" + g.gtype + "/" + vmid
}

// findGuest locates a VMID across the cluster. A VMID that no guest holds comes
// back as a zero guest, which callers read through found.
func (p pve) findGuest(vmid string) (guest, string) {
	resp := p.get("cluster/resources?type=vm")
	if !resp.OK {
		return guest{}, "Failed to list the cluster's guests" + withReason(pveError(resp)) + "."
	}

	found := guest{}
	sdk.GetBytes(resp.Body, "data").ForEach(func(_, v sdk.Result) bool {
		if v.Get("vmid").String() != vmid {
			return true
		}
		found = guest{
			node:   v.Get("node").String(),
			gtype:  v.Get("type").String(),
			status: v.Get("status").String(),
		}
		return false
	})
	return found, ""
}

// pickNode returns the configured node, or the online node with the most free
// memory when the plan leaves the choice to the module.
func (p pve) pickNode(preferred string) (string, string) {
	if preferred != "" {
		return preferred, ""
	}

	resp := p.get("nodes")
	if !resp.OK {
		return "", "Failed to list the cluster's nodes" + withReason(pveError(resp)) + "."
	}

	best := ""
	bestFree := int64(-1)
	sdk.GetBytes(resp.Body, "data").ForEach(func(_, v sdk.Result) bool {
		if v.Get("status").String() != "online" {
			return true
		}
		if free := v.Get("maxmem").Int64() - v.Get("mem").Int64(); free > bestFree {
			best = v.Get("node").String()
			bestFree = free
		}
		return true
	})
	if best == "" {
		return "", "No online Proxmox node is available to deploy on."
	}
	return best, ""
}

// nextVMID allocates a free VMID. Proxmox picks the cluster's next id when the
// plan sets no floor; otherwise the ids already in use are read once and the
// first free id at or above the floor is taken.
func (p pve) nextVMID(start int) (int, string) {
	if start <= 0 {
		resp := p.get("cluster/nextid")
		if !resp.OK {
			return 0, "Failed to allocate a VMID" + withReason(pveError(resp)) + "."
		}
		return sdk.Atoi(sdk.GetBytes(resp.Body, "data").String()), ""
	}

	resp := p.get("cluster/resources?type=vm")
	if !resp.OK {
		return 0, "Failed to list the cluster's guests" + withReason(pveError(resp)) + "."
	}
	taken := make(map[int]bool)
	sdk.GetBytes(resp.Body, "data").ForEach(func(_, v sdk.Result) bool {
		taken[v.Get("vmid").Int()] = true
		return true
	})

	for vmid := start; vmid < start+maxVMIDProbe; vmid++ {
		if !taken[vmid] {
			return vmid, ""
		}
	}
	return 0, "No VMID is free in the " + sdk.Itoa(maxVMIDProbe) + " ids from " + sdk.Itoa(start) + "."
}

// power runs a status action against the guest and waits for the task it
// starts. It reports the reason the action failed, empty on success.
func (p pve) power(g guest, vmid, action string) string {
	resp := p.post(g.path(vmid)+"/status/"+action, nil)
	if !resp.OK {
		return pveError(resp)
	}
	return p.settle(g.node, upidOf(resp))
}

// config reads the guest's current settings, which a rewrite has to build on.
func (p pve) config(g guest, vmid string) (sdk.Result, string) {
	resp := p.get(g.path(vmid) + "/config")
	if !resp.OK {
		return sdk.Result{}, "Failed to read the guest's configuration" + withReason(pveError(resp)) + "."
	}
	return sdk.GetBytes(resp.Body, "data"), ""
}

// applyConfig writes guest settings. QEMU takes its config asynchronously over
// POST; LXC only accepts the synchronous PUT.
func (p pve) applyConfig(g guest, vmid string, body *sdk.JSON) string {
	endpoint := g.path(vmid) + "/config"

	var resp sdk.HTTPResponse
	if g.gtype == "lxc" {
		resp = p.put(endpoint, body.Build())
	} else {
		resp = p.post(endpoint, body.Build())
	}
	if !resp.OK {
		return pveError(resp)
	}
	return p.settle(g.node, upidOf(resp))
}

// settle waits for a task to finish and reports why it failed. A task still
// running when the poll budget runs out is not an error: the operation is
// under way on the hypervisor.
func (p pve) settle(node, upid string) string {
	if upid == "" {
		return ""
	}
	_, failure := p.waitTask(node, upid)
	return failure
}

// waitTask polls a Proxmox task until it stops, reporting whether it finished
// inside the poll budget and, when it did, why it failed.
func (p pve) waitTask(node, upid string) (bool, string) {
	if upid == "" {
		return true, ""
	}

	endpoint := "nodes/" + node + "/tasks/" + sdk.URLEncode(upid) + "/status"
	for attempt := 0; attempt < taskPollAttempts; attempt++ {
		if attempt > 0 {
			sdk.Sleep(taskPollDelayMS)
		}

		resp := p.get(endpoint)
		if !resp.OK {
			continue
		}
		data := sdk.GetBytes(resp.Body, "data")
		if data.Get("status").String() != "stopped" {
			continue
		}
		if exit := data.Get("exitstatus").String(); exit != "" && exit != "OK" {
			return true, exit
		}
		return true, ""
	}
	return false, ""
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// resolve is the opening of every lifecycle call after deploy: a client, the
// VMID HostCore holds for the service, and where that VMID lives right now.
func resolve(args sdk.Args) (pve, guest, string, string) {
	p, msg := newPVE(args)
	if msg != "" {
		return pve{}, guest{}, "", msg
	}

	vmid := vmidOf(args)
	if vmid == "" {
		return pve{}, guest{}, "", "A VMID is required to address the guest."
	}

	g, msg := p.findGuest(vmid)
	if msg != "" {
		return pve{}, guest{}, vmid, msg
	}
	return p, g, vmid, ""
}

// vmidOf reads the VMID HostCore replays for the service. The platform sends it
// as remote_id; the deploy result names it server_id.
func vmidOf(args sdk.Args) string {
	for _, key := range []string{"remote_id", "server_id", "vmid"} {
		if v := sdk.TrimSpace(args.Get(key).String()); v != "" {
			return v
		}
	}
	return ""
}

// upidOf reads the task id Proxmox returns from a mutating call. A call that
// completed inline returns no UPID.
func upidOf(resp sdk.HTTPResponse) string {
	upid := sdk.GetBytes(resp.Body, "data").String()
	if len(upid) < 5 || upid[:5] != "UPID:" {
		return ""
	}
	return upid
}

// pveError extracts the most useful failure text from a Proxmox response: the
// first parameter-level error, then a message field, else the status.
func pveError(resp sdk.HTTPResponse) string {
	first := ""
	sdk.GetBytes(resp.Body, "errors").ForEach(func(k, v sdk.Result) bool {
		first = k.String() + ": " + v.String()
		return false
	})
	if first != "" {
		return first
	}
	if m := sdk.GetBytes(resp.Body, "message").String(); m != "" {
		return m
	}
	return "status " + sdk.Itoa(resp.Status)
}

func withReason(reason string) string {
	if reason == "" {
		return ""
	}
	return " (" + reason + ")"
}

func cfgStr(args sdk.Args, key string) string {
	return sdk.TrimSpace(args.ServiceConfig(key))
}

// cfgBool reads a boolean service_config value, falling back to def when the
// plan leaves the key unset.
func cfgBool(args sdk.Args, key string, def bool) bool {
	v := cfgStr(args, key)
	if v == "" {
		return def
	}
	return sdk.ParseBool(v)
}

// setStr copies a service_config value into the payload under its Proxmox field
// name, leaving the field out when the plan does not set it.
func setStr(body *sdk.JSON, args sdk.Args, key, field string) {
	if v := cfgStr(args, key); v != "" {
		body.Set(field, v)
	}
}

func setInt(body *sdk.JSON, args sdk.Args, key, field string) {
	if v := cfgStr(args, key); v != "" {
		body.Set(field, sdk.Atoi(v))
	}
}

// boolParam renders a boolean as the 0/1 Proxmox expects for its flag
// parameters.
func boolParam(v bool) string {
	if v {
		return "1"
	}
	return "0"
}

// macOf reads the hardware address out of a guest's net0 device. A QEMU device
// leads with "<model>=<mac>"; an LXC one names it hwaddr.
func macOf(config sdk.Result, gtype string) string {
	parts := sdk.SplitCSV(config.Get("net0").String())
	if len(parts) == 0 {
		return ""
	}

	if gtype == "lxc" {
		for _, part := range parts {
			if len(part) > 7 && part[:7] == "hwaddr=" {
				return part[7:]
			}
		}
		return ""
	}

	for i := 0; i < len(parts[0]); i++ {
		if parts[0][i] == '=' {
			return parts[0][i+1:]
		}
	}
	return ""
}

// rootDisk names the guest's root volume: the configured bus for a QEMU disk,
// and always rootfs for a container.
func rootDisk(args sdk.Args, gtype string) string {
	if gtype == "lxc" {
		return "rootfs"
	}
	bus := cfgStr(args, "disk_bus")
	if bus == "" {
		bus = "scsi"
	}
	return bus + "0"
}

func storagePool(args sdk.Args) string {
	if s := cfgStr(args, "storage"); s != "" {
		return s
	}
	return "local-lvm"
}

// guestName is the name the guest carries on the cluster, tying it back to the
// store service it belongs to.
func guestName(args sdk.Args) string {
	if name := cfgStr(args, "hostname"); name != "" {
		return name
	}
	if id := sdk.TrimSpace(args.Get("service_id").String()); id != "" {
		return "vps" + id
	}
	return "vps" + sdk.RandomString(6)
}

// guestPassword is the root password the platform generated for the service,
// falling back to a random one so a guest is never created without credentials.
func guestPassword(args sdk.Args) string {
	if pw := args.Get("password").String(); pw != "" {
		return pw
	}
	return sdk.RandomString(16)
}

// withRealm appends Proxmox's default realm to a bare user name, which the
// access endpoint requires.
func withRealm(user string) string {
	if user == "" {
		return ""
	}
	for i := 0; i < len(user); i++ {
		if user[i] == '@' {
			return user
		}
	}
	return user + "@pam"
}
