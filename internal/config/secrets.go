package config

import (
	"bytes"
	"errors"
	"fmt"

	"gopkg.in/yaml.v3"
)

// SecretMask replaces configured secret values in API responses. Sending this
// exact value back in an update keeps the currently stored secret.
const SecretMask = "********"

// secretPaths lists config keys that must never be returned over HTTP.
var secretPaths = [][]string{
	{"kotak", "mobile_number"},
	{"kotak", "ucc"},
	{"web", "basic_auth", "password"},
	{"ai", "jev", "api_key"},
	{"ai", "agent", "api_key"},
}

// RedactSecrets returns a copy of c with every configured secret masked.
func RedactSecrets(c Config) Config {
	if c.Kotak.MobileNumber != "" {
		c.Kotak.MobileNumber = SecretMask
	}
	if c.Kotak.UCC != "" {
		c.Kotak.UCC = SecretMask
	}
	if c.Web.BasicAuth.Password != "" {
		c.Web.BasicAuth.Password = SecretMask
	}
	if c.AI.Jev.APIKey != "" {
		c.AI.Jev.APIKey = SecretMask
	}
	if c.AI.Agent.APIKey != "" {
		c.AI.Agent.APIKey = SecretMask
	}
	return c
}

// RedactYAMLSecrets masks secret scalar values in a YAML document while
// preserving comments, key order and the rest of the file.
func RedactYAMLSecrets(data []byte) ([]byte, error) {
	doc, err := parseYAMLDocument(data)
	if err != nil {
		return nil, fmt.Errorf("redact yaml secrets: %w", err)
	}
	for _, path := range secretPaths {
		node := findMappingValue(doc, path)
		if node == nil || node.Kind != yaml.ScalarNode || node.Value == "" {
			continue
		}
		node.Value = SecretMask
		node.Tag = "!!str"
	}
	out, err := encodeYAML(doc)
	if err != nil {
		return nil, fmt.Errorf("redact yaml secrets: %w", err)
	}
	return out, nil
}

// RestoreYAMLSecrets replaces masked values in data with the values from the
// currently stored YAML document. When no mask is present, data is returned
// unchanged so normal saves keep their original formatting.
func RestoreYAMLSecrets(data, current []byte) ([]byte, error) {
	doc, err := parseYAMLDocument(data)
	if err != nil {
		return nil, fmt.Errorf("restore yaml secrets: %w", err)
	}

	currentDoc, currentErr := parseYAMLDocument(current)
	changed := false
	for _, path := range secretPaths {
		node := findMappingValue(doc, path)
		if node == nil || node.Kind != yaml.ScalarNode || node.Value != SecretMask {
			continue
		}

		var old *yaml.Node
		if currentErr == nil {
			old = findMappingValue(currentDoc, path)
		}
		if old != nil && old.Kind == yaml.ScalarNode {
			node.Value = old.Value
			node.Tag = old.Tag
			node.Style = old.Style
		} else {
			// Never persist the mask itself as a real credential.
			node.Value = ""
			node.Tag = "!!str"
		}
		changed = true
	}
	if !changed {
		return data, nil
	}

	out, err := encodeYAML(doc)
	if err != nil {
		return nil, fmt.Errorf("restore yaml secrets: %w", err)
	}
	return out, nil
}

func parseYAMLDocument(data []byte) (*yaml.Node, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, errors.New("empty yaml")
	}
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if len(doc.Content) == 0 {
		return nil, errors.New("empty yaml")
	}
	return &doc, nil
}

func encodeYAML(doc *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func findMappingValue(node *yaml.Node, path []string) *yaml.Node {
	current := node
	for _, key := range path {
		if current.Kind == yaml.DocumentNode {
			if len(current.Content) == 0 {
				return nil
			}
			current = current.Content[0]
		}
		if current.Kind == yaml.AliasNode {
			current = current.Alias
		}
		if current == nil || current.Kind != yaml.MappingNode {
			return nil
		}
		var next *yaml.Node
		for i := 0; i+1 < len(current.Content); i += 2 {
			if current.Content[i].Value == key {
				next = current.Content[i+1]
				break
			}
		}
		if next == nil {
			return nil
		}
		current = next
	}
	return current
}
