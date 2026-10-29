
Reference rewriting: `lode task add --body-file` and `lode task edit --body-file` walk the parsed markdown for image destinations that are local relative paths (no scheme, no leading `/`), resolve them against the body file's directory, upload each, and rewrite the destination to `/blob/<hash>` before the body is sent. Only images are rewritten; a local file link is a warning. Absolute paths and schemes are untouched. Traversal above the body file's directory is an error. A missing file fails the whole command before the task is written. `--body` does not rewrite. `--no-upload` opts out.

