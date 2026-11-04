# Use Traefik as a reverse proxy

You can set up Traefik in two ways:

<details>
<summary>Using Docker labels</summary>

Add these labels to your `docker-compose.yml` file:

```yml
labels:
  - traefik.http.routers.codes.rule=Host(`codes.example.com`) # Change to your subdomain
  # Uncomment the line below if you run Codes in a subdirectory
  # - traefik.http.routers.codes.rule=PathPrefix(`/codes{regex:$$|/.*}`) # Use the path prefix for your chosen subdirectory
  - traefik.http.routers.codes.entrypoints=websecure # Change to the name of your 443 port entrypoint
  - traefik.http.routers.codes.tls.certresolver=lets-encrypt # Change to certresolver's name
  - traefik.http.routers.codes.service=codes
  - traefik.http.services.codes.loadBalancer.server.port=6157
```
</details>
<details>
<summary>Using a <code>yml</code> file</summary>

> [!Note]
> Don't forget to change the `<server-address>` to your server's IP

`traefik_dynamic.yml`
```yml
http:
  routers:
    codes:
      entrypoints: websecure
      rule: Host(`codes.example.com`) # Comment this line and uncomment the line below if using a subpath
      # rule: PathPrefix(`/codes{regex:$$|/.*}`) # Use the path prefix for your chosen subdirectory
      # middlewares:
      #   - codes-fail2ban
      service: codes
      tls:
        certresolver: lets-encrypt
  services:
    codes:
      loadbalancer:
        servers:
          - url: "http://<server-address>:6157"

```

</details>
