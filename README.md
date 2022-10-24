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
configuration file must be a JSON file with the following format:

``` json
{
    "name": "my-beloved-app",
    "addr": "127.0.0.1:12000",
    "upstream": "127.0.0.1:12001"
    "rate": "10M",
    "per_client": false,
    "debug": false
}
```
