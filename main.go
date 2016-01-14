package main

import (
	"errors"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/DarkDNA/vultr"

	"github.com/mitchellh/multistep"
	"github.com/mitchellh/packer/common"
	"github.com/mitchellh/packer/helper/config"
	"github.com/mitchellh/packer/helper/communicator"
	"github.com/mitchellh/packer/packer"
	"github.com/mitchellh/packer/packer/plugin"
	"github.com/mitchellh/packer/template/interpolate"
)

const DefaultOs = "Debian 7 x64 (wheezy)"
const DefaultPlan = "768 MB RAM,15 GB SSD,1.00 TB BW"
const DefaultRegion = "Atlanta"
const BuilderId = "askholme.vultr"

type Config struct {
	common.PackerConfig `mapstructure:",squash"`
	APIKey              string `mapstructure:"api_key"`
	Region              string `mapstructure:"region"`
	Plan                string `mapstructure:"plan"`
	Os                  string `mapstructure:"os"`
	OsSnapshot          string `mapstructure:"os_snapshot"`
	SnapshotName        string `mapstructure:"snapshot_name"`
	IpxeUrl             string `mapstructure:"ipxe"`

	PrivateNetworking bool   `mapstructure:"private_networking"`
	IPv6              bool   `mapstructure:"IPv6"`
	SSHUsername       string `mapstructure:"ssh_username"`
	SSHPassword       string `mapstructure:"ssh_password"`
	SSHPrivateKey     string `mapstructure:"ssh_key"`
	SSHPort           uint   `mapstructure:"ssh_port"`

	RawSSHTimeout   string `mapstructure:"ssh_timeout"`
	RawStateTimeout string `mapstructure:"state_timeout"`

	// These are unexported since they're set by other fields
	// being set.
	sshTimeout   time.Duration
	stateTimeout time.Duration
	tpl          *interpolate.Context
}

// Assume this implements packer.Builder
type Builder struct {
	config Config
	runner multistep.Runner
}

func (b *Builder) Prepare(raws ...interface{}) ([]string, error) {
	err := config.Decode(&b.config, &config.DecodeOpts{
		Interpolate: true,
	}, raws...)
	if err != nil {
		return nil, err
	}
	b.config.tpl.UserVariables = b.config.PackerUserVars

	// Accumulate any errors
	var errs *packer.MultiError

	// Optional configuration with defaults
	if b.config.APIKey == "" {
		// Default to environment variable for api_key, if it exists
		b.config.APIKey = os.Getenv("VULTR_API_KEY")
	}

	if b.config.Region == "" {
		b.config.Region = DefaultRegion
	}

	if b.config.Plan == "" {
		b.config.Plan = DefaultPlan
	}

	if b.config.Os == "" {
		b.config.Os = DefaultOs
	}

	if b.config.SnapshotName == "" {
		// Default to packer-{{ unix timestamp (utc) }}
		b.config.SnapshotName = "packer-{{timestamp}}"
	}

	if b.config.SSHUsername == "" {
		// Default to "root". You can override this if your
		// SourceImage has a different user account then the DO default
		b.config.SSHUsername = "root"
	}

	if b.config.SSHPort == 0 {
		// Default to port 22 per DO default
		b.config.SSHPort = 22
	}

	if b.config.RawSSHTimeout == "" {
		// Default to 1 minute timeouts
		b.config.RawSSHTimeout = "1m"
	}

	if b.config.RawStateTimeout == "" {
		// Default to 6 minute timeouts waiting for
		// desired state. i.e waiting for droplet to become active
		b.config.RawStateTimeout = "6m"
	}

	// Required configurations that will display errors if not set
	if b.config.APIKey == "" {
		errs = packer.MultiErrorAppend(
			errs, errors.New("an api_key must be specified"))
	}
	if (b.config.OsSnapshot != "" || b.config.IpxeUrl != "") && (b.config.SSHPassword == "" && b.config.SSHPrivateKey == "") {
		errs = packer.MultiErrorAppend(
			errs, errors.New("SSH Password or private key must be provided when building from snapshot or custom OS"))
	}
	sshTimeout, err := time.ParseDuration(b.config.RawSSHTimeout)
	if err != nil {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("Failed parsing ssh_timeout: %s", err))
	}
	b.config.sshTimeout = sshTimeout

	stateTimeout, err := time.ParseDuration(b.config.RawStateTimeout)
	if err != nil {
		errs = packer.MultiErrorAppend(
			errs, fmt.Errorf("Failed parsing state_timeout: %s", err))
	}
	b.config.stateTimeout = stateTimeout

	if errs != nil && len(errs.Errors) > 0 {
		return nil, errs
	}
	return nil, nil
}

func (b *Builder) Run(ui packer.Ui, hook packer.Hook, cache packer.Cache) (packer.Artifact, error) {
	// Initialize the DO API client
	client, err := vultr.NewClient(b.config.APIKey)
	if err != nil {
		return nil, err
	}
	// Set up the state
	state := new(multistep.BasicStateBag)
	state.Put("config", b.config)
	state.Put("client", client)
	state.Put("hook", hook)
	state.Put("ui", ui)

	// Build the steps
	steps := []multistep.Step{
		new(stepCreateServer),
		new(stepServerInfo),
		&communicator.StepConnectSSH{
			Host:     sshAddress,
			SSHConfig:      sshConfig,
		},
		new(common.StepProvision),
		new(stepShutdown),
		new(stepHalt),
		new(stepSnapshot),
	}

	// Run the steps
	if b.config.PackerDebug {
		b.runner = &multistep.DebugRunner{
			Steps:   steps,
			PauseFn: common.MultistepDebugFn(ui),
		}
	} else {
		b.runner = &multistep.BasicRunner{Steps: steps}
	}

	b.runner.Run(state)

	// If there was an error, return that
	if rawErr, ok := state.GetOk("error"); ok {
		return nil, rawErr.(error)
	}

	if _, ok := state.GetOk("snapshot_name"); !ok {
		log.Println("Failed to find snapshot_name in state. Bug?")
		return nil, nil
	}

	var region string
	//  GetId ensures we have an idea and then we get the label. We ignore errors because they would have shown earlier
	region_id, _ := client.Params.GetId("region", b.config.Region)
	region, _ = client.Params.GetLabel("region", region_id)

	if err != nil {
		return nil, err
	}

	artifact := &Artifact{
		snapshotName: state.Get("snapshot_name").(string),
		snapshotId:   state.Get("snapshot_id").(string),
		regionName:   region,
		client:       client,
	}
	return artifact, nil
}

func (b *Builder) Cancel() {
	if b.runner != nil {
		log.Println("Cancelling the step runner...")
		b.runner.Cancel()
	}
}

func main() {
	server, err := plugin.Server()
	if err != nil {
		panic(err)
	}
	server.RegisterBuilder(new(Builder))
	server.Serve()
}
