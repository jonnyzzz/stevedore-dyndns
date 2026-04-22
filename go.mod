module github.com/jonnyzzz/stevedore-dyndns

// Language version matches the toolchain (Dockerfile, workflow) running Go 1.26.
// 9seconds/mtg v2 requires go 1.26 in its own go.mod, so consumers must declare
// at least 1.26 to build against it.
go 1.26

require (
	github.com/9seconds/mtg/v2 v2.2.8
	github.com/cloudflare/cloudflare-go v0.86.0
	github.com/fsnotify/fsnotify v1.7.0
	github.com/gorilla/websocket v1.5.3
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/OneOfOne/xxhash v1.2.8 // indirect
	github.com/goccy/go-json v0.10.2 // indirect
	github.com/google/go-cmp v0.7.0 // indirect
	github.com/google/go-querystring v1.1.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.5 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/ncruces/go-dns v1.3.3 // indirect
	github.com/panjf2000/ants/v2 v2.12.0 // indirect
	github.com/pires/go-proxyproto v0.11.0 // indirect
	github.com/tylertreat/BoomFilters v0.0.0-20251117164519-53813c36cc1b // indirect
	github.com/yl2chen/cidranger v1.0.2 // indirect
	golang.org/x/crypto v0.49.0 // indirect
	golang.org/x/net v0.52.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.42.0 // indirect
	golang.org/x/text v0.35.0 // indirect
	golang.org/x/time v0.5.0 // indirect
)
