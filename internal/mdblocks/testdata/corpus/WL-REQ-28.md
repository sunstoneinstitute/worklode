
A worker image tag is a digest over its inputs: the base image digest, `.worklode/Dockerfile`, and the pin files the bootstrap reads (`go.mod`, `scripts/tailwind.sha256`). A starting worker requests the tag its checkout implies. Finding it is the fast path. Not finding it is the build trigger. No change detector exists because a tag that exists is by construction built from those inputs.

