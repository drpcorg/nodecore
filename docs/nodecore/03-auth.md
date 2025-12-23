# Auth config guide

The `auth` section defines how clients authenticate with nodecore and optionally enforces fine-grained access restrictions.

```yaml
auth:
  enabled: true
  request-strategy:
    type: token
    token:
      value: "my-token"
#    type: jwt
#    jwt:
#      public-key: /path/to/key
#      allowed-issuer: "my-iss"
#      expiration-required: true
  key-management:
    - id: "my-first-key"
      type: local
      local:
        key: "bXkta2V5"
        settings:
          allowed-ips:
            - "192.0.0.1"
            - "127.0.0.1"
          methods:
            allowed:
              - 'eth_getBlockByNumber'
            forbidden:
              - "eth_syncing"
          contracts:
            allowed:
              - "0xfde26a190bfd8c43040c6b5ebf9bc7f8c934c80a"
```

By default, `auth` is disabled.

## Fields

* `enabled` - Enables or disables authentication globally. **_Default_**: `false`

### request-strategy

```yaml
request-strategy:
type: token
token:
  value: "my-token"
#type: jwt
#jwt:
#  public-key: /path/to/key
#  allowed-issuer: "my-iss"
#  expiration-required: true
```

The `request-strategy` section defines the primary way clients authenticate against nodecore.
It acts as the first line of access control, applied to every request before it reaches any upstream.

You can choose between two strategies:
1. Token-based authentication – the simplest option, where a single static token is configured. Clients must include this token in their requests. This is useful for internal services, testing, or controlled environments.
2. JWT-based authentication – a more advanced option that uses signed JSON Web Tokens. This allows you to integrate with external identity providers, enforce expiration checks, and validate issuers. It is suited for multi-tenant or production environments where stronger guarantees are required.

`request-strategy` fields:
* `type` - Authentication method (`token` or `jwt`)

if `type: token`, you mush provide:
```yaml
token:
  value: "my-token"
```
* `token.value` - The static token string that clients must provide with their requests. Typically used for simple setups, testing, or environments where a single shared key is acceptable. Should be passed via the `X-Nodecore-Token` header. **_Required_**

if `type: jwt`, you mush provide:
```yaml
jwt:
  public-key: /path/to/key
  allowed-issuer: "my-iss"
  expiration-required: true
```
* `jwt.public-key` - Path to a PEM/DER encoded public key file used to verify JWT signatures. **_Required_**
* `jwt.allowed-issuer` - Restricts accepted JWTs to those issued by the specified `iss` claim. **_Default_**: "" that means that any `iss` claim is allowed 
* `jwt.expiration-required` - If set to true, every JWT must contain a valid `exp` claim. **_Default_**: `false`

JWT should be passed via the `Authorization` header with the `Bearer` prefix.

### key-management

```yaml
key-management:
- id: "drpc-super-key"
  type: drpc
  drpc:
    owner:
      id: "id"
      api-token: "apiToken"  
- id: "my-first-key"
  type: local
  local:
    key: "bXkta2V5"
    settings:
      allowed-ips:
        - "192.0.0.1"
        - "127.0.0.1"
      methods:
        allowed:
          - 'eth_getBlockByNumber'
        forbidden:
          - "eth_syncing"
      contracts:
        allowed:
          - "0xfde26a190bfd8c43040c6b5ebf9bc7f8c934c80a"
```

The `key-management` section defines scoped access keys that provide fine-grained control over how clients interact with nodecore.

Each key can be tied to specific IP addresses, allowed or forbidden RPC methods, and contract whitelists.
This is especially useful for multi-tenant environments or when you want to enforce stricter rules per application.

`key-management` fields:
* `id` - Unique identifier for the key. **_Required_**, **_Unique_**
* `type` - Defines the backend that manages this key. Currently supported: `local`, `drpc`. **_Required_**

#### Local keys

If `type: local`, you mush provide:
```yaml
local:
  key: "bXkta2V5"
  settings:
    cors-origins:
      - "https://example.com"
    allowed-ips:
      - "192.0.0.1"
      - "127.0.0.1"
    methods:
      allowed:
        - 'eth_getBlockByNumber'
      forbidden:
        - "eth_syncing"
    contracts:
      allowed:
        - "0xfde26a190bfd8c43040c6b5ebf9bc7f8c934c80a"
```

The `local` key type is the simplest form of key management. It allows you to define access keys directly in the configuration file, without relying on an external service. This is useful for quick setups and internal environments.

* `key` - The actual access key value. Any string. Should be passed via the `"X-Nodecore-Key"` header OR via a path param in your URL (`https://your-site.com/queries/ethereum/api-key/bXkta2V5`). **_Required_**, **_Unique_**
* `settings.allowed-ips` - Restricts key usage to the listed IP addresses. When validating requests, nodecore determines the client IPs in the following order:
  * It first checks the `X-Forwarded-For` header (commonly set by proxies or load balancers). If present, all comma-separated values are collected as candidate IPs
  * If the header is missing or empty, it falls back to the remote address of the TCP connection
  * If the remote address cannot be parsed, the request is assumed to come from `127.0.0.1`
* `settings.methods.allowed` - A whitelist of RPC methods that can be called with this key
* `settings.methods.forbidden` - A blacklist of RPC methods that cannot be called with this key
* `settings.contracts.allowed` - Restricts interaction to a specific set of contract addresses for `eth_call` and `eth_getLogs` methods
* `settings.cors-origins` - The list of allowed CORS origins for this key. If present, nodecore will include the appropriate `Access-Control-Allow-Origin` header only for the origins explicitly listed here. If the incoming request’s Origin header does not match any entry, the request will be rejected by the CORS layer.

#### DRPC keys

DRPC keys are owned and maintained on the DRPC platform and fetched by nodecore through the DRPC integration API. Such keys allow you to offload key lifecycle operations to the external platform, specifically to DRPC. A single DRPC account may contain multiple owners (teams), and each owner can maintain its own set of NodeCore keys. Nodecore always treats DRPC keys identically to local keys during request validation.

Use DRPC-managed keys if you want:
- Centralized management of all nodecore keys across multiple nodecore instances.
- Separation of keys by project/team (each owner has its own namespace).
- Automatic synchronization: once an owner updates/deletes a key in DRPC, nodecore sees the change without requiring configuration edits.
- (**_Future feature_**) Aggregated analytics in DRPC: dashboards with nodecore stats: request counts, latency, error rate, per-key insights, etc.

If `type: drpc`, you mush provide:
```yaml
drpc:
  owner:
    id: "you-owner-id"
    api-token: "apiToken"
```

* `owner.id` - The Owner ID from your DRPC nodecore workspace. Keys belonging to this owner will be accessible in nodecore. **_Required_**
* `owner.api-token` - API token used to authenticate this nodecore instance when calling DRPC. It can be generated and regenerated on the DRPC website. **_Required_**

Each DRPC key entry corresponds to one owner on DRPC.
If your account manages multiple owners, list them all under `key-management`.

All key-level restrictions — IP whitelists, method filters, contract filters, CORS origins, etc. — are configured on the DRPC website, not in `nodecore.yaml`. Nodecore automatically fetches these attributes from DRPC and enforces them exactly the same way as for locally defined keys.

Supported attributes:
* **IP whitelist** - Restrict which client IPs may use this key. Nodecore rejects requests from any IP not included.
* **Allowed RPC methods** - Allow or deny specific RPC methods (e.g., allow only eth_call / block eth_sendRawTransaction).
* **Allowed contract addresses** - Restrict access to smart contracts by address
* **CORS origins** - Restrict browser origins allowed to use this key.

How DRPC key integration works:
1. **Integration Configuration Check**. On startup, nodecore verifies that the `integration.drpc` section is present in the configuration.
   * If the section is missing, nodecore fails to start with an error. DRPC-managed keys cannot function without a valid integration endpoint.
2. **DRPC Key Configuration Validation**. Nodecore inspects each `auth.key-management` entry of type `drpc`. For every such key, the following fields are required: `owner.id`, `owner.api-token`.  If either field is missing, empty, or malformed, nodecore terminates with a configuration error.
3. **Key Load from DRPC**. Once configuration is validated, nodecore loads the owner’s keys from DRPC periodically (every 1 minute):
   * All the requests are authenticated using the owner’s api-token.
   * All key definitions and attributes (IP whitelist, method filters, etc.) are fetched and stored locally.
   * Once nodecore receives a response from DRPC, it may:
       * update key attributes (IP whitelist, methods, CORS, contracts, enabled/disabled state)
       * add new keys created on DRPC
       * remove keys deleted on DRPC

> **⚠️ Important**: 
> 1. If at least one key is defined in the `key-management` section, then every request must include a valid nodecore key. If no key is provided, or the provided key does not match the configured rules, the request will be rejected.
> 2. If you want to pass your key via the URL you have to use another endpoint path - `/queries/{chain}/api-key/{your-key}`