package main

// version and commit are set at build time via .goreleaser.yml's ldflags
// (-X main.version=... -X main.commit=...); "dev" is the value for a
// plain `go build`/`go run` outside a release.
var (
	version = "dev"
	commit  = "none"
)

func main() {
	Execute()
}
