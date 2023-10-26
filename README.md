# Description #

`rlproxy` is a small TCP proxy with rate limiting capability. It uses
a [token bucket](https://en.wikipedia.org/wiki/Token_bucket) algorithm
to apply a rate limit on the bandwidth allocated to downstream
clients. The rate limit can be applied globally across all downstream
clients or per client.

# Usage #

`rlproxy` can be configured using command line flags or a
configuration file or both. Type `rlproxy -h` to find out the flags
supported by `rlproxy`. Settings specified using command line flags
take precedence over settings in the configuration file. The
configuration file can be a JSON file with the following format:

``` json
{
    "name": "my-beloved-app",
    "addrs": [
        "127.0.0.1:12000"
    ],
    "upstream": "127.0.0.1:12001",
    "rate": "10M",
    "burst": "64KiB",
    "per_client": false,
    "no_splice": false,
    "buf_size": "1 Mi",
    "logging": {
        "level": "info",
        "console": {
            "enabled": true,
            "pretty": true,
            "use_stderr": false

        },
        "syslog": {
            "enabled": false,
            "facility": "local0"
        }
    }
}
```

or a YAML file:

``` yaml
name: my-beloved-app
addrs:
  - 127.0.0.1:12000
upstream: 127.0.0.1:12001
rate: 10M
burst: 64KiB
per_client: false
no_splice: false
buf_size: 1 Mi
logging:
  level: info
  console:
    enabled: true
    pretty: true
    use_stderr: false
  syslog:
    enabled: false
    facility: local0
```
