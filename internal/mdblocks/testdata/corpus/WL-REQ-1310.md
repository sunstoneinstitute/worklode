
The [SPA](/projects/worklode/term/spa)'s source lives in `web/app/` in the worklode repo. Node.js and pnpm
are needed to build it and nowhere else. The templ pages' build, the Go
build and every served page stay free of Node.js (WL-REQ-334).

`go build` and `go test` do not need Node.js. The embed directory holds a
committed placeholder `index.html` that says the SPA was not built. The
container image build and CI run `pnpm build` first and replace it with the
real output. The build output is not committed, since hashed file names
change on every build.
