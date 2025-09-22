# Server config guide

The `server` section controls how nodecore runs as a service: listening ports, TLS configuration, and optional profiling/observability.

```yaml
server:
  port: 9090
  metrics-port: 9093
  pprof-port: 6061
  tls:
    enabled: true
    certificate: /path
    key: /path
  pyroscope-config:
    enabled: true
    url: pyrosope-url
    username: pyro-username
    password: pyro-password
```

## Fields

* `port` - The main HTTP port where nodecore listens for incoming RPC requests. **_Default_**: `9090`
* `metrics-port` - Port exposing Prometheus metrics (endpoint `GET /metrics`). By default, it's disabled, so it's necessary to specify the port explicitly to enable prom metrics
* `pprof-port` - Port for Go [pprof](https://github.com/google/pprof) profiling endpoints. By default, profiling is disabled; to enable it, you must explicitly set this port
* `pyroscope-config` - Optional integration with [Pyroscope](https://pyroscope.io/) for continuous profiling
  * `enabled` - enable/disable Pyroscope integration. **_Default_**: `false`
  * `url`: URL of the Pyroscope server. **_Required_** if `enabled: true`
  * `username`, `password`: authentication credentials. **_Required_** if `enabled: true`