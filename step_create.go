package main

import (
	"fmt"

	"github.com/DarkDNA/vultr"
	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/packer"
)

type stepCreateServer struct {
	serverId string
}

func (s *stepCreateServer) Run(state multistep.StateBag) multistep.StepAction {
	client := state.Get("client").(*vultr.Client)
	ui := state.Get("ui").(packer.Ui)
	c := state.Get("config").(Config)

	var err error
	var keyId string
	if c.SSHPrivateKey != "" {
		ui.Say("Uploading SSH key")
		keyId, err = client.CreateSSHKey(name, c.SSHPrivateKey)

		if err != nil {
			err := fmt.Errorf("Error uploading ssh key: %s", err)
			state.Put("error", err)
			ui.Error(err.Error())
			return multistep.ActionHalt
		}
	}
	ui.Say("Creating server...")

	// Create the droplet based on configuration
	opts := client.CreateOpts()
	opts.Region = c.Region
	opts.Plan = c.Plan
	opts.Os = c.Os
	opts.PrivateNet = c.PrivateNetworking
	opts.IpV6 = c.IPv6
	opts.IpxeUrl = c.IpxeUrl
	if c.SSHPrivateKey != "" {
		opts.SSHKeyId = keyId
	}
	serverId, err := client.CreateServer(&opts)

	if err != nil {
		err := fmt.Errorf("Error creating server: %s", err)
		state.Put("error", err)
		ui.Error(err.Error())
		return multistep.ActionHalt
	}

	// We use this in cleanup
	s.serverId = serverId

	// Store the droplet id for later
	state.Put("server_id", serverId)

	return multistep.ActionContinue
}

func (s *stepCreateServer) Cleanup(state multistep.StateBag) {
	// If the id isn't there, we probably never created it
	if s.serverId == "" {
		return
	}

	client := state.Get("client").(*vultr.Client)
	ui := state.Get("ui").(packer.Ui)

	// Destroy the server we just created
	ui.Say("Destroying server...")

	err := client.DeleteServer(s.serverId)
	if err != nil {
		ui.Error(fmt.Sprintf(
			"Error destroying server. Please destroy it manually, id is: %v - error was %s", s.serverId, err))
	}
}
