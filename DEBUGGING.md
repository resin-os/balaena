# Step-by-step debugging

First, get into the development environment, exposing the port we'll use for the
debugger:

```ssh
make DOCKER_PORT=40000:40000 BIND_DIR=. shell
```

From the development environment, you shall be able to run Delve (`dlv`) in
headless mode as you please. For example, to debug the tests under
`./daemon/images` you'd do this:

```sh
GO111MODULE=off dlv --listen=:40000 --headless=true --api-version=2 test ./daemon/images
```

This will wait until a remote debugger connects to port 40000.

How to run Delve on your host to connect to the remote debugger depends on what
editor, IDE or Delve front end you are using (if you are using one at all). On
VS Code, a proper entry on `.vscode/lauch.json` would look like this:

```json
{
    "name": "Attach",
    "type": "go",
    "request": "attach",
    "mode": "remote",
    "port": 40000,
    "host": "127.0.0.1",
    "cwd": "${workspaceFolder}",
    "remotePath": "/go/src/github.com/docker/docker",
    "showLog": true,
}
```
