# Description #

`rlproxy` is a small TCP proxy with rate limiting capability. It uses
a [token bucket](https://en.wikipedia.org/wiki/Token_bucket) algorithm
to apply a rate limit on the bandwidth allocated to downstream
clients. The rate limit can be applied globally across all downstream
clients or per client.

# Usage #

`rlproxy` can be configured using command line flags. Type `rlproxy
-h` to find out the flags supported by `rlproxy`.
