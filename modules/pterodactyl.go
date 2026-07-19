// HostCore Service Module - Pterodactyl

package main

import sdk "github.com/hostcore-co/HostCore.ModuleSDK"

// TestModuleConnection verifies that the stores connection to the external application.
//
//go:wasmexport TestModuleConnection
func TestModuleConnection() int32 {
	args := sdk.GetArgs()

	resp := sdk.HTTPGet("https://"+args.Get("domain").String()+"/api/application/nests?per_page=1", pteroHeaders(args.Get("api_key").String()))

	if !resp.OK {
		return sdk.Fail("Connection failed", resp.Body)
	}

	return sdk.Success(sdk.NewJSON().Set("status", resp.Status).Build())
}

// GetServiceConfigOptions returns the schema of configurable fields for this
// module. HostCore uses this to render the service configuration UI.
//
//go:wasmexport GetServiceConfigOptions
func GetServiceConfigOptions() int32 {
	return sdk.Success([]byte(`[
		{"order":1,"label":"Nest","type":"number","key":"nest_id","description":"","regex":"^[0-9]+$","required":true},
		{"order":2,"label":"Egg","type":"number","key":"egg_id","description":"","regex":"^[0-9]+$","required":true},
		{"order":3,"label":"Location","type":"number","key":"location_id","description":"","regex":"^[0-9]+$","required":true},
		{"order":4,"label":"Server Name","type":"string","key":"server_name","description":"","regex":"","required":true},
		{"order":5,"label":"CPU Limit","type":"number","key":"cpu","description":"","regex":"^[0-9]+$","required":true},
		{"order":6,"label":"Memory (MB)","type":"number","key":"memory","description":"","regex":"^[0-9]+$","required":true},
		{"order":7,"label":"Disk (MB)","type":"number","key":"disk","description":"","regex":"^[0-9]+$","required":true},
		{"order":8,"label":"Block IO Weight","type":"number","key":"io","description":"","regex":"^[0-9]+$","required":true},
		{"order":9,"label":"Swap (MB)","type":"number","key":"swap","description":"","regex":"^[0-9]+$","required":true},
		{"order":10,"label":"Database Limit","type":"number","key":"databases","description":"","regex":"^[0-9]+$","required":true},
		{"order":11,"label":"Backup Limit","type":"number","key":"backups","description":"","regex":"^[0-9]+$","required":true},
		{"order":12,"label":"Port Map","type":"string","key":"port_map","description":"JSON map of named port ranges e.g. {\"SERVER_PORT\":\"28015-28085\"}","regex":"","required":true},
		{"order":13,"label":"Disable OOM Killer","type":"boolean","key":"oom_disabled","description":"","regex":"","required":false},
		{"order":14,"label":"Allocations","type":"number","key":"allocations","description":"","regex":"^[0-9]+$","required":true}
	]`))
}

// DeployService is called when a store triggers the service.deploy event.
//
//go:wasmexport DeployService
func DeployService() int32 {
	//args := sdk.GetArgs()
	//user := args.User()
	return 0
}

func getOrCreatePteroUser(user sdk.User, baseURL string, hdrs map[string]string) (int64, string) {
	resp := sdk.HTTPGet(baseURL+"/api/application/users?filter[email]="+user.Email, hdrs)
	if !resp.OK && resp.Status != 404 {
		return 0, "failed to search users: status " + sdk.Itoa(resp.Status)
	}

	results := sdk.GetBytes(resp.Body, "data")
	if results.IsArray() && len(results.Array()) > 0 {
		var matchedID int64
		results.ForEach(func(_, item sdk.Result) bool {
			if item.Get("attributes.email").String() == user.Email {
				matchedID = item.Get("attributes.id").Int64()
				return false
			}
			return true
		})
		if matchedID > 0 {
			return matchedID, ""
		}
	}

	body := sdk.NewJSON().
		Set("email", user.Email).
		Set("username", sdk.RandomString(12)).
		Set("first_name", user.FirstName).
		Set("last_name", user.LastName).
		Set("root_admin", false).
		Set("external_id", user.ID).
		Build()

	createResp := sdk.HTTPPost(baseURL+"/api/application/users", hdrs, body)
	if !createResp.OK {
		detail := sdk.GetBytes(createResp.Body, "errors.0.detail").String()
		if detail == "" {
			detail = "status" + sdk.Itoa(createResp.Status)
		}
		return 0, "failed to create user: " + detail
	}

	id := sdk.GetBytes(createResp.Body, "attributes.id").Int64()
	if id == 0 {
		return 0, "failed to read newly created pterodactyl user id"
	}
	return id, ""
}

// findNodeWithAllocations iterates deployable nodes, filters by location id, and checks each nodes allocations against the port map. Returns the first node that satisfies all the port ranges
func findNodeWithAllocations(nodes sdk.Result, locationID string, portMap sdk.Result, baseURL string, hdrs map[string]string) (defaultAllocID int64, additionalAllocIDs []int64, found bool) {
	nodes.ForEach(func(_, node sdk.Result) bool {
		attr := node.Get("attributes")
		if attr.Get("location_id").String() != locationID {
			return false // wrong location, try next
		}

		nodeID := attr.Get("id").Int64()
		allocsResp := sdk.HTTPGet(baseURL+"/api/application/nodes/"+sdk.Itoa64(nodeID), hdrs)
		if !allocsResp.OK {
			return true // can't read allocations try the next node
		}

		allocs := sdk.GetBytes(allocsResp.Body, "data")
		d, additionalAllocs, ok := matchAllocationsToPortMap(allocs, portMap)
		if !ok {
			return true // no matching free allocations on the node
		}

		defaultAllocID = d
		additionalAllocIDs = additionalAllocs
		found = true
		return false
	})
	return
}

func matchAllocationsToPortMap(allocs sdk.Result, portMap sdk.Result) (int64, []int64, bool) {
	type freeAlloc struct {
		id   int64
		port int
	}

	var free []freeAlloc
	allocs.ForEach(func(_, item sdk.Result) bool {
		attr := item.Get("attributes")
		if attr.Get("assigned").Bool() {
			return true // already in use
		}
		id := attr.Get("id").Int64()
		port := int(attr.Get("port").Int())
		if id > 0 && port > 0 {
			free = append(free, freeAlloc{id: id, port: port})
		}
		return true
	})

	used := make(map[int]bool, len(free))
	var defaultID int64
	var additionalIDs []int64

	// consume one free allocation from the given port range
	consume := func(key, rangeStr string) bool {
		start, end, ok := sdk.ParsePortRange(rangeStr)
		if !ok {
			return false
		}
		for i, a := range free {
			if used[i] {
				continue
			}
			if a.port >= start && a.port <= end {
				used[i] = true
				if key == "SERVER_PORT" {
					defaultID = a.id
				} else {
					additionalIDs = append(additionalIDs, a.id)
				}
				return true
			}
		}
		return false
	}

	// SERVER_PORT must be handled first — it becomes the default allocation
	if sp := portMap.Get("SERVER_PORT").String(); sp != "" {
		if !consume("SERVER_PORT", sp) {
			return 0, nil, false
		}
	}

	// process all remaining port map keys
	allOK := true
	portMap.ForEach(func(k, v sdk.Result) bool {
		if k.String() == "SERVER_PORT" {
			return true // already handled
		}
		if !consume(k.String(), v.String()) {
			allOK = false
			return false
		}
		return true
	})

	if !allOK || defaultID == 0 {
		return 0, nil, false
	}
	return defaultID, additionalIDs, true
}

// buildServerPayload constructs the full Pterodactyl server creation JSON body.
func buildServerPayload(
	name string,
	userID int64,
	eggID, dockerImage, startup, externalID string,
	memory, disk, cpu, swap, io, oomDisabled string,
	databases, allocations, backups string,
	defaultAllocID int64,
	additionalAllocIDs []int64,
	eggVars sdk.Result,
) []byte {
	// environment: populate from egg variable defaults
	var envBuf []byte
	envBuf = append(envBuf, '{')
	firstEnv := true
	eggVars.ForEach(func(_, item sdk.Result) bool {
		attr := item.Get("attributes")
		k := attr.Get("env_variable").String()
		v := attr.Get("default_value").String()
		if k != "" {
			if !firstEnv {
				envBuf = append(envBuf, ',')
			}
			envBuf = append(envBuf, (k)...)
			envBuf = append(envBuf, ':')
			envBuf = append(envBuf, sdk.JsonString(v)...)
			firstEnv = false
		}
		return true
	})
	envBuf = append(envBuf, '}')

	// additional allocations array
	var addlBuf []byte
	addlBuf = append(addlBuf, '[')
	for i, id := range additionalAllocIDs {
		if i > 0 {
			addlBuf = append(addlBuf, ',')
		}
		addlBuf = append(addlBuf, []byte(sdk.Itoa64(id))...)
	}
	addlBuf = append(addlBuf, ']')

	oomVal := []byte("false")
	if oomDisabled == "true" {
		oomVal = []byte("true")
	}

	limits := sdk.NewJSON().
		Set("memory", sdk.Atoi(memory)).
		Set("swap", sdk.Atoi(swap)).
		Set("disk", sdk.Atoi(disk)).
		Set("io", sdk.Atoi(io)).
		Set("cpu", sdk.Atoi(cpu)).
		Set("oom_disabled", oomVal).
		Build()

	featureLimits := sdk.NewJSON().
		Set("databases", sdk.Atoi(databases)).
		Set("allocations", sdk.Atoi(allocations)).
		Set("backups", sdk.Atoi(backups)).
		Build()

	allocation := sdk.NewJSON().
		Set("default", []byte(sdk.Itoa64(defaultAllocID))).
		Set("additional", addlBuf).
		Build()

	return sdk.NewJSON().
		Set("name", name).
		Set("user", []byte(sdk.Itoa64(userID))).
		Set("egg", sdk.Atoi(eggID)).
		Set("docker_image", dockerImage).
		Set("startup", startup).
		Set("external_id", externalID).
		Set("start_on_completion", true).
		Set("environment", envBuf).
		Set("limits", limits).
		Set("feature_limits", featureLimits).
		Set("allocation", allocation).
		Build()
}

// pteroHeaders returns the standard headers required by the Pterodactyl API
func pteroHeaders(apiKey string) map[string]string {
	return map[string]string{
		"Authorization": "Bearer " + apiKey,
		"Accept":        "application/json",
		"Content-Type":  "application/json",
	}
}
