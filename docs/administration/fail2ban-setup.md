# Fail2ban setup

Fail2ban can be used to ban IPs that try to bruteforce the login page.
Log level must be set at least to `warn`.

Add this filter in `/etc/fail2ban/filter.d/codes.conf`:
```ini
[Definition]
failregex =  Invalid .* authentication attempt from <HOST>
ignoreregex =
```

Add this jail in `/etc/fail2ban/jail.d/codes.conf`:
```ini
[codes]
enabled = true
filter = codes
logpath = /home/*/.opengist/log/opengist.log
maxretry = 10
findtime = 3600
bantime = 600
banaction = iptables-allports
port = anyport
```

Then run
```shell
service fail2ban restart
```
