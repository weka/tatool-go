package config

// Config holds all runtime configuration built from CLI flags.
type Config struct {
	// Mode
	K8s bool
	SSM bool

	// SSH fields
	IPs         []string
	User        string
	Password    string
	PasswordEnv string
	PasswordFile string
	KeyFile     string
	UseDzdo     bool

	// K8s fields
	Namespace   string
	ClusterName string
	Kubeconfig  string

	// SSM fields
	AWSRegion   string
	AWSProfile  string
	InstanceIDs []string

	// Script selection
	ScriptNums  []int
	Interactive bool
	ListOnly    bool

	// Output
	LogDir      string
	Compression string // "gz" or "bz2"

	// Script source
	ScriptsPath string // override for external scripts dir
}
