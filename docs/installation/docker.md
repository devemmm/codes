# Install with Docker

Codes Docker images are published to the [GitHub Container Registry](https://github.com/devemmm/codes/pkgs/container/codes).

When replacing an existing OpenGist container, keep mounting its current host
data directory to `/opengist`. The container path and `OG_OPENGIST_HOME`
environment variable remain stable for compatibility. Use `$HOME/.codes` for a
new installation if you want Codes-branded host paths.

```shell
docker pull ghcr.io/devemmm/codes:latest
```

It can be used in a `docker-compose.yml` file :

1. Create a `docker-compose.yml` file with the following content
2. Run `docker compose up -d`
3. Codes is now running on port 6157, browse http://localhost:6157

```yml
services:
  codes:
    image: ghcr.io/devemmm/codes:latest
    container_name: codes
    restart: unless-stopped
    ports:
      - "6157:6157" # HTTP port
      - "2222:2222" # SSH port, can be removed if you don't use SSH
    volumes:
      # Keep /opengist as the container data path for compatibility.
      - "$HOME/.codes:/opengist"
    environment:
      # OG_LOG_LEVEL: info
      # other configuration options
```

You can define which user/group should run the container and own the files by setting the `UID` and `GID` environment
variables :

```yml
services:
  codes:
    # ...
    environment:
      UID: 1001
      GID: 1001
```

## Rootless

By default the container starts as `root` and the entrypoint drops privileges to the
user defined by `UID`/`GID` (see above). 

If you'd rather have the container run as a
non-root user from the start — for example with `user:` in Compose, or under rootless
Docker/Podman — set the `user` key instead:

```yml
services:
  codes:
    # ...
    user: "1001:1001"
    volumes:
      - "./codes-data:/opengist"
```

In this mode the entrypoint runs Codes directly as that user.
Create the Codes data directory and own it on the host first:
```shell
mkdir -p ./codes-data && sudo chown -R 1001:1001 ./codes-data
```

