package config

import (
	"time"

	v1alpha1 "github.com/bubustack/bobrapet/api/v1alpha1"
	corev1 "k8s.io/api/core/v1"
)

// Config defines the engram configuration decoded from spec.with.
type Config struct {
	Transport string        `json:"transport" mapstructure:"transport"`
	Server    ServerConfig  `json:"server" mapstructure:"server"`
	Stdio     StdioConfig   `json:"stdio" mapstructure:"stdio"`
	MCP       MCPInitConfig `json:"mcp" mapstructure:"mcp"`
	Policies  []Policy      `json:"policies" mapstructure:"policies"`
}

// ServerConfig configures the Streamable HTTP client.
type ServerConfig struct {
	BaseURL           string            `json:"baseURL" mapstructure:"baseURL"`
	Headers           map[string]string `json:"headers" mapstructure:"headers"`
	HeadersFromSecret map[string]string `json:"headersFromSecret" mapstructure:"headersFromSecret"`
}

// StdioConfig configures the runner Pod and attach behavior.
type StdioConfig struct {
	Image           string                      `json:"image" mapstructure:"image"`
	ImagePullPolicy string                      `json:"imagePullPolicy" mapstructure:"imagePullPolicy"`
	Command         []string                    `json:"command" mapstructure:"command"`
	Args            []string                    `json:"args" mapstructure:"args"`
	Resources       *v1alpha1.WorkloadResources `json:"resources" mapstructure:"resources"`
	Security        *v1alpha1.WorkloadSecurity  `json:"security" mapstructure:"security"`
	NodeSelector    map[string]string           `json:"nodeSelector" mapstructure:"nodeSelector"`
	Tolerations     []corev1.Toleration         `json:"tolerations" mapstructure:"tolerations"`
	PodLabels       map[string]string           `json:"podLabels" mapstructure:"podLabels"`
	PodAnnotations  map[string]string           `json:"podAnnotations" mapstructure:"podAnnotations"`
	DeletionPolicy  string                      `json:"deletionPolicy" mapstructure:"deletionPolicy"`
	//nolint:lll // Field name/tag pair must match the manifest schema.
	TerminationGracePeriodSeconds *int64 `json:"terminationGracePeriodSeconds" mapstructure:"terminationGracePeriodSeconds"`
	UseEphemeralSecret            bool   `json:"useEphemeralSecret" mapstructure:"useEphemeralSecret"`
}

// MCPInitConfig forwards client capabilities on Initialize.
type MCPInitConfig struct {
	InitClientCapabilities map[string]any `json:"initClientCapabilities" mapstructure:"initClientCapabilities"`
	// PostInitDelay adds a wait after initialize+ListTools probe, giving MCP servers
	// time to complete async setup (e.g. Discord gateway login). Example: "5s".
	PostInitDelay time.Duration `json:"postInitDelay" mapstructure:"postInitDelay"`
	// CallToolRetries is the number of retry attempts for callTool when the server
	// returns isError:true with a transient message. Defaults to 0 (no retries).
	CallToolRetries int `json:"callToolRetries" mapstructure:"callToolRetries"`
	// CallToolRetryDelay is the delay between callTool retries. Defaults to 2s.
	CallToolRetryDelay time.Duration `json:"callToolRetryDelay" mapstructure:"callToolRetryDelay"`
}

// Policy describes how the adapter should react to Story triggers.
type Policy struct {
	Name         string            `json:"name" mapstructure:"name"`
	Description  string            `json:"description,omitempty" mapstructure:"description"`
	StoryName    string            `json:"storyName" mapstructure:"storyName"`
	Events       []string          `json:"events,omitempty" mapstructure:"events"`
	Rooms        []string          `json:"rooms,omitempty" mapstructure:"rooms"`
	Participants []string          `json:"participants,omitempty" mapstructure:"participants"`
	StoryInputs  map[string]any    `json:"storyInputs,omitempty" mapstructure:"storyInputs"`
	Metadata     map[string]string `json:"metadata,omitempty" mapstructure:"metadata"`
}
