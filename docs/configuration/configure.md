# Configuration

Codes provides flexible configuration options through either a YAML file and/or environment variables.
You would only need to specify the configuration options you want to change — for any config option left untouched, 
Codes will simply apply the default values.

The [configuration cheat sheet](cheat-sheet.md) lists all available configuration options.


## Configuration via YAML file

The configuration file must be specified when launching the application, using the `--config` flag followed by the path 
to your YAML file.

Usage with Docker Compose :
```yml
services:
  codes:
    # ...
    volumes:
    # ...
    - "/path/to/config.yml:/config.yml"
```

Usage via command line :
```shell
./codes --config /path/to/config.yml
```

You can start by copying and/or modifying the provided [config.yml](https://github.com/devemmm/codes/blob/master/config.yml) file.


## Configuration via Environment Variables

Usage with Docker Compose :
```yml
services:
  codes:
    # ...
    environment:
      OG_LOG_LEVEL: "info"
      # etc.
```

Usage via command line :
```shell
OG_LOG_LEVEL=info ./codes
```

### Using Docker Compose secrets

You can use Docker Compose secrets to keep sensitive information out of your Compose file, using a `.env` file. The entrypoint accepts both `codes_secrets` and the historical `opengist_secrets` secret name.

```dotenv
# file secrets.env
OG_GITLAB_CLIENT_KEY=your_gitlab_client_key
OG_GITLAB_SECRET=your_gitlab_secret_key
```

And then use it in your compose file :

```yml
services:
  codes:
    # ...
    secrets:
      - codes_secrets

secrets:
  codes_secrets:
    file: ./secrets.env
```
