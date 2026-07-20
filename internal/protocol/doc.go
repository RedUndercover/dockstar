// Package protocol contains generated Wayland client bindings.
//
//go:generate sh -c "go run hazelnut.eclair.cafe/wlcl/cmd/wlclgen@v1.1.2 -package protocol -out protocol_gen.go ../../protocols/wayland.xml ../../protocols/kde-output-device-v2.xml ../../protocols/kde-output-management-v2.xml && gofmt -w protocol_gen.go"
package protocol
