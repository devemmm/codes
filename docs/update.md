# Update Codes

## Make a backup

Before updating, back up the application home directory, where all the data is stored. Existing installations keep their current data directory and configuration names after upgrading.

The default home directory for existing installs is `~/.opengist`; retain that directory unless you deliberately migrate it and update the configuration.

```shell
cp -r ~/.opengist ~/.opengist.bak
```

## Install the new version

### With Docker

Pull the latest Codes image
```shell
docker pull ghcr.io/devemmm/codes:latest
```

And restart the container, using `docker compose up -d` for example if you use docker compose.

### Via binary

Stop the running instance, then download the matching archive from the [Codes Releases](https://github.com/devemmm/codes/releases/latest) page and extract it.

```shell
tar xzvf codesVERSION-linux-amd64.tar.gz # Replace VERSION with the release number
cd codes
chmod +x codes
./codes # with or without `--config config.yml`
```

### From source

Stop the running instance, then pull the latest changes from the Codes branch and build the new version.

```shell
git switch codes
git pull
make
./codes
```

## Restore the backup

If you have any issue with the new version, you can restore the backup you made before updating.

```shell
rm -rf ~/.opengist
cp -r ~/.opengist.bak ~/.opengist
```

Then run the previous Codes version again.
