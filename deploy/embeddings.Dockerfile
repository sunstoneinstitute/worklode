# The embedding server with its weights baked in.
#
# Stock text-embeddings-inference resolves --model-id against huggingface.co at
# pod start, which makes an outage a convergence outage, needs a Hugging Face
# token in every namespace, and re-downloads 1.3 GB on every restart. Baking the
# weights makes the pull a build-time concern: one CI secret, and the running
# pod holds no Hugging Face credential at all.
#
# google/embeddinggemma-300m is gated ("gated: manual"), so the build needs a
# token from an account granted access under the Gemma Terms of Use.
#
# The token arrives as a BuildKit secret mount, never as an ARG or an ENV. Both
# of those persist: a build arg is recorded verbatim in the image's history and
# `docker history` prints it back, and an ENV stays in the image config. A
# secret mount is a tmpfs that exists only for the RUN that asks for it and is
# never committed to a layer. The build workflow checks the published config for
# the token anyway.
#
# Built by .github/workflows/build-embeddings-image.yml, by hand, once per model
# version — not on every commit. Bump MODEL_ID or the base tag and the workflow's
# default tag together.

FROM python:3.13-slim AS weights
ARG MODEL_ID=google/embeddinggemma-300m
RUN pip install --no-cache-dir huggingface_hub==1.30.0
# --local-dir, not the hub cache: text-embeddings-inference takes a filesystem
# path for --model-id and then never consults the hub at all, so there is no
# cache layout or lock file for a read-only root filesystem to trip over.
#
# snapshot_download leaves download metadata (etags, commit hashes) under
# /model/.cache; the server never reads it, so it does not travel to the final
# stage.
RUN --mount=type=secret,id=hf_token \
    HF_TOKEN="$(cat /run/secrets/hf_token)" python -c "\
import os; from huggingface_hub import snapshot_download; \
snapshot_download(os.environ['MODEL_ID'], local_dir='/model')" \
    && rm -rf /model/.cache

# A fresh stage: nothing from the build environment reaches the published image
# except the weights copied on the next line.
FROM ghcr.io/huggingface/text-embeddings-inference:cpu-1.9.3
COPY --from=weights /model /model
