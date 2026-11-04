# Run with systemd

For non-Docker deployments, Codes can run as a systemd service.

## System service

Install the Codes binary and configuration:

```shell
sudo install -m 0755 codes /usr/local/bin/codes
sudo install -d -m 0755 /etc/codes
sudo install -m 0644 config.yml /etc/codes/config.yml
```

Create a dedicated system user and configure the data home:

```shell
sudo useradd --system --home-dir /var/lib/codes --shell /usr/sbin/nologin codes
sudo install -d -o codes -g codes /var/lib/codes
sudo chown -R codes:codes /var/lib/codes
```

The application still accepts the historical `opengist-home` configuration
key for compatibility:

```yaml
opengist-home: /var/lib/codes
```

Create `/etc/systemd/system/codes.service`:

```ini
[Unit]
Description=Codes Server
After=network.target

[Service]
Type=simple
User=codes
Group=codes
ExecStart=/usr/local/bin/codes --config /etc/codes/config.yml
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Enable and start the service:

```shell
sudo systemctl daemon-reload
sudo systemctl enable --now codes
sudo systemctl status codes
```

When upgrading an existing installation, keep its existing service unit, user,
configuration, and data directory unless you have planned a separate migration.

## User service

This example assumes Codes was built from source and the shell user is `pastebin`.
Adapt the paths and account name for your system.

Enable lingering and create the systemd user directory:

```shell
loginctl enable-linger
mkdir -p /home/pastebin/.config/systemd/user
```

Create `/home/pastebin/.config/systemd/user/codes.service`:

```ini
[Unit]
Description=Codes Server
After=network.target

[Service]
Type=simple
ExecStart=/home/pastebin/codes/codes --config /home/pastebin/codes/config.yml
Restart=on-failure

[Install]
WantedBy=default.target
```

Enable and start the user service:

```shell
systemctl --user daemon-reload
systemctl --user enable --now codes
systemctl --user status codes
```
