package main

import (
	"fmt"

	"github.com/mitchellh/multistep"
	"golang.org/x/crypto/ssh"
)

func sshAddress(state multistep.StateBag) (string, error) {
	config := state.Get("config").(Config)
	ipAddress := state.Get("server_ip").(string)

	return fmt.Sprintf("%s:%d", ipAddress, config.SSHPort), nil
}

func sshConfig(state multistep.StateBag) (*ssh.ClientConfig, error) {
	config := state.Get("config").(Config)
	clientConfig := ssh.ClientConfig{User: config.SSHUsername}
	if config.OsSnapshot == "" && config.IpxeUrl == "" {
		// default case where vultr generated the password
		password := state.Get("default_password").(string)
		clientConfig.Auth = []ssh.AuthMethod{ssh.Password(password)}
	} else if config.SSHPassword != "" {
		// special case but we got a password
		clientConfig.Auth = []ssh.AuthMethod{ssh.Password(config.SSHPassword)}
	} else {
		// special case and we got a key
		signer, err := ssh.ParsePrivateKey([]byte(config.SSHPrivateKey))
		if err != nil {
			return nil, fmt.Errorf("Error setting up SSH config: %s", err)
		}
		clientConfig.Auth = []ssh.AuthMethod{ssh.PublicKeys(signer)}
	}
	return &clientConfig, nil
}
