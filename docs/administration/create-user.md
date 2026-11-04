# Seed a user account (CLI)

When deploying Codes in an automated way (Ansible, Terraform, a bootstrap script, …), you usually want an administrator account to exist right after installation, without having to go through the web setup form. The `admin create-user` command does exactly that.

Public signup is disabled until an administrator exists. Create the first administrator from the command line before exposing the web interface. Once signed in, an administrator can enable public registration in the admin panel.

## Usage

```bash
./codes admin create-user [options]
```

| Option             | Description                                                                                          |
|--------------------|------------------------------------------------------------------------------------------------------|
| `--username`, `-u` | Username of the new user (same validation rules as the web form: alphanumerics and dashes, ≤ 24).    |
| `--password`, `-p` | Password for the new user. Note: visible in the process list, so prefer `--password-stdin` for automation. |
| `--password-stdin` | Read the password from the first line of stdin. Keeps the secret out of the process list and shell history. |
| `--email`          | Optional email address (enables [Gravatar](https://gravatar.com)).                                   |
| `--admin`          | Grant administrator privileges to the new user.                                                      |

All inputs are flags (including the username). Flags may be given in any order.

## Seeding an admin

```bash
$ ./codes admin create-user --username admin --password 's3cret!' --admin --email admin@example.com
User admin has been created with administrator privileges.
```

Reading the password from stdin is recommended for automation, so it never appears in the process list or your playbook:

```bash
$ printf '%s' "$OPENGIST_ADMIN_PASSWORD" | ./codes admin create-user --username admin --password-stdin --admin
User admin has been created with administrator privileges.
```

### Idempotency

The command is idempotent: if the user already exists it does nothing and exits successfully (`0`). This makes it safe to run repeatedly from a provisioning playbook.

```bash
$ ./codes admin create-user --username admin --password 's3cret!' --admin
User admin already exists; nothing to do.
```

::: tip
`create-user` only creates accounts. To change an existing user's password use [`admin reset-password`](./reset-password), and to grant or revoke admin rights use [`admin toggle-admin`](./manage-admins).
:::

## Ansible example

```yaml
- name: Ensure the Codes admin user exists
  shell: |
    printf '%s' '{{ opengist_admin_password }}' | \
    {{ opengist_binary }} admin create-user \
      --username {{ opengist_admin_user }} \
      --password-stdin \
      {% if opengist_admin_email %}--email {{ opengist_admin_email }}{% endif %} \
      --admin
  register: seed
  changed_when: "'has been created' in seed.stdout"
```
