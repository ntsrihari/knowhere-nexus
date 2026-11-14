// This file only stops Go tooling (go vet ./..., go test ./...) from walking
// into the frontend and node_modules. The frontend is not Go code.
module github.com/ntsrihari/knowhere-nexus/frontend-ignore

go 1.27.0
