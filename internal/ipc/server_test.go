package ipc

import (
	"encoding/xml"
	"testing"
)

func TestIntrospectionPublishesSuspendNow(t *testing.T) {
	t.Parallel()
	var node struct {
		Interfaces []struct {
			Name    string `xml:"name,attr"`
			Methods []struct {
				Name string `xml:"name,attr"`
			} `xml:"method"`
		} `xml:"interface"`
	}
	if err := xml.Unmarshal([]byte(introspectionXML), &node); err != nil {
		t.Fatalf("parse introspection XML: %v", err)
	}
	for _, iface := range node.Interfaces {
		if iface.Name != Interface {
			continue
		}
		for _, method := range iface.Methods {
			if method.Name == "SuspendNow" {
				return
			}
		}
		t.Fatalf("%s does not publish SuspendNow", Interface)
	}
	t.Fatalf("%s is missing from introspection XML", Interface)
}
