package sandbox

import (
	"iter"
	"maps"
	"net"
	"slices"
	"strings"
)

// ReservedEnv are set by the adapter and cannot be overridden by job secrets
var ReservedEnv = []string{"UMP_BROKER_URL", "UMP_TOKEN", "UMP_RUN_ID", "UMP_DNS"}

// ProxyEnv are the variables that send a sandbox's traffic through the egress proxy
// HTTP tools read the HTTP_PROXY pair and ALL_PROXY points the rest at the SOCKS5 proxy on the same port
// Node only honors them with NODE_USE_ENV_PROXY, and both spellings are set since tools read one or the other
var ProxyEnv = []string{"HTTP_PROXY", "HTTPS_PROXY", "http_proxy", "https_proxy", "ALL_PROXY", "all_proxy", "NO_PROXY", "no_proxy", "NODE_USE_ENV_PROXY"}

// SSHConfig sends every SSH connection of a proxied sandbox through the egress proxy, so git over SSH works without a route of its own
// Distributions include /etc/ssh/ssh_config.d from their ssh_config, and a user's own ~/.ssh/config still takes precedence
var SSHConfig = "# Written by Umpteenth: SSH connections leave the sandbox through the egress proxy\nHost *\n  ProxyCommand " + UmpBinary + " connect %h %p\n"

// SSHConfigPath is where proxied sandboxes get SSHConfig
const SSHConfigPath = "/etc/ssh/ssh_config.d/ump.conf"

// Proxied reports whether a sandbox reaches the outside through the egress proxy, since it has no route of its own
func Proxied(network NetworkPolicy) bool {
	return network == NetworkInternet || network == NetworkAllowlist
}

// BrokerEnv merges the job's secrets with the broker variables, which always win
// broker is the host:port the sandbox reaches the broker and its egress proxy on, and dns replaces the resolvers of unrestricted sandboxes, which ump init writes to /etc/resolv.conf
func BrokerEnv(spec Spec, broker string, dns []string) []string {
	viaProxy := Proxied(spec.Network)
	keys := slices.Sorted(maps.Keys(spec.Env))
	env := make([]string, 0, len(keys)+len(ReservedEnv)+len(ProxyEnv))
	for _, key := range keys {
		if key == "" || strings.Contains(key, "=") || slices.Contains(ReservedEnv, key) || (viaProxy && slices.Contains(ProxyEnv, key)) {
			continue
		}
		env = append(env, key+"="+spec.Env[key])
	}
	env = append(env,
		"UMP_BROKER_URL=http://"+broker,
		"UMP_TOKEN="+spec.Broker.Token,
		"UMP_RUN_ID="+spec.RunID,
	)

	// Only unrestricted sandboxes resolve names themselves, since the others leave that to the proxy
	if len(dns) > 0 && spec.Network == NetworkUnrestricted {
		env = append(env, "UMP_DNS="+strings.Join(dns, ","))
	}

	// The egress proxy shares the broker listener and knows the sandbox by its token
	// socks5h makes clients leave name resolution to the proxy, since the sandbox can't resolve public names itself
	if viaProxy {
		for key, value := range ProxyVars(spec.Broker.Token, broker) {
			env = append(env, key+"="+value)
		}
		env = append(env, "NODE_USE_ENV_PROXY=1")
	}
	return env
}

// ProxyVars are the proxy variables, in the order of ProxyEnv, that point a client at the egress proxy under a token
// Image builds pass them as build arguments, while sandboxes get them in their environment
func ProxyVars(token, broker string) iter.Seq2[string, string] {
	credentials := "ump:" + token + "@" + broker
	proxy, socks := "http://"+credentials, "socks5h://"+credentials
	host, _, err := net.SplitHostPort(broker)
	if err != nil {
		host = broker
	}
	noProxy := host + ",localhost,127.0.0.1"
	values := map[string]string{
		"HTTP_PROXY": proxy, "HTTPS_PROXY": proxy, "http_proxy": proxy, "https_proxy": proxy,
		"ALL_PROXY": socks, "all_proxy": socks, "NO_PROXY": noProxy, "no_proxy": noProxy,
	}
	return func(yield func(string, string) bool) {
		for _, key := range ProxyEnv {
			value, ok := values[key]
			if ok && !yield(key, value) {
				return
			}
		}
	}
}
