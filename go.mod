module github.com/jonnyzzz/stevedore-dyndns

// Language version matches the toolchain (Dockerfile, workflow) running Go 1.26.
// 9seconds/mtg v2 requires go 1.26 in its own go.mod, so consumers must declare
// at least 1.26 to build against it.
go 1.26

require (
	github.com/cloudflare/cloudflare-go v0.86.0
	github.com/fsnotify/fsnotify v1.7.0
	github.com/gorilla/websocket v1.5.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.5 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/mattn/go-colorable v0.1.14 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/rogpeppe/go-internal v1.14.1 // indirect
	github.com/stretchr/testify v1.11.1 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	golang.org/x/time v0.5.0 // indirect
)
