package kubernetes

import (
	"encoding/base64"
	"fmt"
	"strings"

	"gopkg.in/yaml.v2"

	"github.com/VictoriaMetrics/VictoriaMetrics/lib/fs"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/promauth"
	"github.com/VictoriaMetrics/VictoriaMetrics/lib/proxy"
)

// apiConfig contains config for API server
type apiConfig struct {
	aw *apiWatcher
}

// Config represent configuration file for kubernetes API server connection
// https://github.com/kubernetes/client-go/blob/master/tools/clientcmd/api/v1/types.go#L28
type Config struct {
	Kind       string `yaml:"kind,omitempty"`
	APIVersion string `yaml:"apiVersion,omitempty"`
	Clusters   []struct {
		Name    string   `yaml:"name"`
		Cluster *Cluster `yaml:"cluster"`
	} `yaml:"clusters"`
	AuthInfos []struct {
		Name     string    `yaml:"name"`
		AuthInfo *AuthInfo `yaml:"user"`
	} `yaml:"users"`
	Contexts []struct {
		Name    string   `yaml:"name"`
		Context *Context `yaml:"context"`
	} `yaml:"contexts"`
	CurrentContext string `yaml:"current-context"`
}

// Cluster contains information about how to communicate with a kubernetes cluster
type Cluster struct {
	Server                   string     `yaml:"server"`
	TLSServerName            string     `yaml:"tls-server-name,omitempty"`
	InsecureSkipTLSVerify    bool       `yaml:"insecure-skip-tls-verify,omitempty"`
	CertificateAuthority     string     `yaml:"certificate-authority,omitempty"`
	CertificateAuthorityData string     `yaml:"certificate-authority-data,omitempty"`
	ProxyURL                 *proxy.URL `yaml:"proxy-url,omitempty"`
}

// AuthInfo contains information that describes identity information.  This is use to tell the kubernetes cluster who you are.
type AuthInfo struct {
	ClientCertificate     string `yaml:"client-certificate,omitempty"`
	ClientCertificateData string `yaml:"client-certificate-data,omitempty"`
	ClientKey             string `yaml:"client-key,omitempty"`
	ClientKeyData         string `yaml:"client-key-data,omitempty"`
	// TODO add support for it
	Exec                 *ExecConfig `yaml:"exec,omitempty"`
	Token                string      `yaml:"token,omitempty"`
	TokenFile            string      `yaml:"tokenFile,omitempty"`
	Impersonate          string      `yaml:"act-as,omitempty"`
	ImpersonateUID       string      `yaml:"act-as-uid,omitempty"`
	ImpersonateGroups    []string    `yaml:"act-as-groups,omitempty"`
	ImpersonateUserExtra []string    `yaml:"act-as-user-extra,omitempty"`
	Username             string      `yaml:"username,omitempty"`
	Password             string      `yaml:"password,omitempty"`
}

func (au *AuthInfo) validate() error {
	errContext := "field: %s is not supported currently, open an issue with feature request for it"
	if au.Exec != nil {
		return fmt.Errorf(errContext, "exec")
	}
	if len(au.ImpersonateUID) > 0 {
		return fmt.Errorf(errContext, "act-as-uid")
	}
	if len(au.Impersonate) > 0 {
		return fmt.Errorf(errContext, "act-as")
	}
	if len(au.ImpersonateGroups) > 0 {
		return fmt.Errorf(errContext, "act-as-groups")
	}
	if len(au.ImpersonateUserExtra) > 0 {
		return fmt.Errorf(errContext, "act-as-user-extra")
	}
	if len(au.Password) > 0 && len(au.Username) == 0 {
		return fmt.Errorf("username cannot be empty, if password defined")
	}
	return nil
}

// ExecConfig contains information about os.command, that returns auth token for kubernetes cluster connection
type ExecConfig struct {
	// Command to execute.
	Command string `json:"command"`
	// Arguments to pass to the command when executing it.
	Args []string `json:"args"`
	// Env defines additional environment variables to expose to the process. These
	// are unioned with the host's environment, as well as variables client-go uses
	// to pass argument to the plugin.
	Env []ExecEnvVar `json:"env"`

	// Preferred input version of the ExecInfo. The returned ExecCredentials MUST use
	// the same encoding version as the input.
	APIVersion string `json:"apiVersion,omitempty"`

	// This text is shown to the user when the executable doesn't seem to be
	// present. For example, `brew install foo-cli` might be a good InstallHint for
	// foo-cli on Mac OS systems.
	InstallHint string `json:"installHint,omitempty"`

	// ProvideClusterInfo determines whether or not to provide cluster information,
	// which could potentially contain very large CA data, to this exec plugin as a
	// part of the KUBERNETES_EXEC_INFO environment variable. By default, it is set
	// to false. Package k8s.io/client-go/tools/auth/exec provides helper methods for
	// reading this environment variable.
	ProvideClusterInfo bool `json:"provideClusterInfo"`

	// InteractiveMode determines this plugin's relationship with standard input. Valid
	// values are "Never" (this exec plugin never uses standard input), "IfAvailable" (this
	// exec plugin wants to use standard input if it is available), or "Always" (this exec
	// plugin requires standard input to function). See ExecInteractiveMode values for more
	// details.
	//
	// If APIVersion is client.authentication.k8s.io/v1alpha1 or
	// client.authentication.k8s.io/v1beta1, then this field is optional and defaults
	// to "IfAvailable" when unset. Otherwise, this field is required.
	//+optional
	InteractiveMode string `json:"interactiveMode,omitempty"`
}

// ExecEnvVar is used for setting environment variables when executing an exec-based
// credential plugin.
type ExecEnvVar struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// Context is a tuple of references to a cluster and AuthInfo
type Context struct {
	Cluster  string `yaml:"cluster"`
	AuthInfo string `yaml:"user"`
}

type kubeConfig struct {
	basicAuth *promauth.BasicAuthConfig
	server    string
	token     string
	tokenFile string
	tlsConfig *promauth.TLSConfig
	proxyURL  *proxy.URL
}

func buildConfig(sdc *SDConfig) (*kubeConfig, error) {

	data, err := fs.ReadFileOrHTTP(sdc.KubeConfig)
	if err != nil {
		return nil, fmt.Errorf("cannot read kubeConfig from %q: %w", sdc.KubeConfig, err)
	}
	var config Config
	if err = yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("cannot parse %q: %w", sdc.KubeConfig, err)
	}

	authInfos := make(map[string]*AuthInfo)
	for _, obj := range config.AuthInfos {
		authInfos[obj.Name] = obj.AuthInfo
	}
	clusterInfos := make(map[string]*Cluster)
	for _, obj := range config.Clusters {
		clusterInfos[obj.Name] = obj.Cluster
	}
	contexts := make(map[string]*Context)
	for _, obj := range config.Contexts {
		contexts[obj.Name] = obj.Context
	}

	contextName := config.CurrentContext
	configContext := contexts[contextName]
	if configContext == nil {
		return nil, fmt.Errorf("context %q does not exist", contextName)
	}

	clusterInfoName := configContext.Cluster
	configClusterInfo := clusterInfos[clusterInfoName]
	if configClusterInfo == nil {
		return nil, fmt.Errorf("cluster %q does not exist", clusterInfoName)
	}

	if len(configClusterInfo.Server) == 0 {
		return nil, fmt.Errorf("kubernetes server address cannot be empty, define it for context: %s", contextName)
	}

	authInfoName := configContext.AuthInfo
	configAuthInfo := authInfos[authInfoName]
	if authInfoName != "" && configAuthInfo == nil {
		return nil, fmt.Errorf("auth info %q does not exist", authInfoName)
	}

	var tlsConfig *promauth.TLSConfig
	var basicAuth *promauth.BasicAuthConfig
	var token, tokenFile string
	isHTTPS := strings.HasPrefix(configClusterInfo.Server, "https://")

	if isHTTPS {
		tlsConfig = &promauth.TLSConfig{
			CAFile:             configClusterInfo.CertificateAuthority,
			ServerName:         configClusterInfo.TLSServerName,
			InsecureSkipVerify: configClusterInfo.InsecureSkipTLSVerify,
		}
	}

	if len(configClusterInfo.CertificateAuthorityData) > 0 && isHTTPS {
		tlsConfig.CA, err = base64.StdEncoding.DecodeString(configClusterInfo.CertificateAuthorityData)
		if err != nil {
			return nil, fmt.Errorf("cannot base64-decode configClusterInfo.CertificateAuthorityData %q: %w", configClusterInfo.CertificateAuthorityData, err)
		}
	}

	if configAuthInfo != nil {
		if err := configAuthInfo.validate(); err != nil {
			return nil, fmt.Errorf("invalid user auth configuration for context: %s, err: %w", contextName, err)
		}
		if isHTTPS {
			tlsConfig.CertFile = configAuthInfo.ClientCertificate
			tlsConfig.KeyFile = configAuthInfo.ClientKey

			if len(configAuthInfo.ClientCertificateData) > 0 {
				tlsConfig.Cert, err = base64.StdEncoding.DecodeString(configAuthInfo.ClientCertificateData)
				if err != nil {
					return nil, fmt.Errorf("cannot base64-decode configAuthInfo.ClientCertificateData %q: %w", configClusterInfo.CertificateAuthorityData, err)
				}
			}
			if len(configAuthInfo.ClientKeyData) > 0 {
				tlsConfig.Key, err = base64.StdEncoding.DecodeString(configAuthInfo.ClientKeyData)
				if err != nil {
					return nil, fmt.Errorf("cannot base64-decode configAuthInfo.ClientKeyData %q: %w", configClusterInfo.CertificateAuthorityData, err)
				}
			}
		}

		if len(configAuthInfo.Username) > 0 || len(configAuthInfo.Password) > 0 {
			basicAuth = &promauth.BasicAuthConfig{
				Username: configAuthInfo.Username,
				Password: promauth.NewSecret(configAuthInfo.Password),
			}
		}
		token = configAuthInfo.Token
		tokenFile = configAuthInfo.TokenFile
	}

	kc := kubeConfig{
		basicAuth: basicAuth,
		server:    configClusterInfo.Server,
		token:     token,
		tokenFile: tokenFile,
		tlsConfig: tlsConfig,
		proxyURL:  configClusterInfo.ProxyURL,
	}

	return &kc, nil
}
