package vm

import "slices"

// FirewallProtocol selects the IP protocol that one firewall rule permits.
type FirewallProtocol string

const (
	// FirewallProtocolAny permits all IP protocols.
	FirewallProtocolAny FirewallProtocol = "any"
	// FirewallProtocolTCP permits Transmission Control Protocol traffic.
	FirewallProtocolTCP FirewallProtocol = "tcp"
	// FirewallProtocolUDP permits User Datagram Protocol traffic.
	FirewallProtocolUDP FirewallProtocol = "udp"
	// FirewallProtocolICMP permits Internet Control Message Protocol traffic.
	FirewallProtocolICMP FirewallProtocol = "icmp"
)

// IsValid reports whether protocol is supported by the VM firewall.
func (protocol FirewallProtocol) IsValid() bool {
	switch protocol {
	case FirewallProtocolAny, FirewallProtocolTCP, FirewallProtocolUDP, FirewallProtocolICMP:
		return true
	default:
		return false
	}
}

// FirewallRule permits one protocol and port range for a set of IP prefixes.
type FirewallRule struct {
	Protocol FirewallProtocol `json:"protocol"`
	Ports    string           `json:"ports,omitempty"`
	CIDRs    []string         `json:"cidrs"`
}

// Equal reports whether two firewall rules contain the same ordered values.
func (rule FirewallRule) Equal(other FirewallRule) bool {
	return rule.Protocol == other.Protocol && rule.Ports == other.Ports && slices.Equal(rule.CIDRs, other.CIDRs)
}

// FirewallConfiguration is the complete desired firewall for one VM.
type FirewallConfiguration struct {
	Enabled  bool           `json:"enabled"`
	Inbound  []FirewallRule `json:"inbound"`
	Outbound []FirewallRule `json:"outbound"`
}

// Equal reports whether two firewall configurations contain the same ordered rules.
func (configuration FirewallConfiguration) Equal(other FirewallConfiguration) bool {
	return configuration.Enabled == other.Enabled &&
		slices.EqualFunc(configuration.Inbound, other.Inbound, FirewallRule.Equal) &&
		slices.EqualFunc(configuration.Outbound, other.Outbound, FirewallRule.Equal)
}

// clone returns a firewall configuration that shares no rule slices with the source.
func (configuration FirewallConfiguration) clone() FirewallConfiguration {
	configuration.Inbound = cloneFirewallRules(configuration.Inbound)
	configuration.Outbound = cloneFirewallRules(configuration.Outbound)
	return configuration
}

func cloneFirewallRules(rules []FirewallRule) []FirewallRule {
	cloned := slices.Clone(rules)
	for index := range cloned {
		cloned[index].CIDRs = slices.Clone(cloned[index].CIDRs)
	}
	return cloned
}
