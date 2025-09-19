# Auth config guide

The `auth` section defines how clients authenticate with dSheltie and optionally enforces fine-grained access restrictions.

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

The `request-strategy` section defines the primary way clients authenticate against dSheltie.
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
* `token.value` - The static token string that clients must provide with their requests. Typically used for simple setups, testing, or environments where a single shared key is acceptable. Should be passed via the `X-Dsheltie-Token` header. **_Required_**

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

The `key-management` section defines scoped access keys that provide fine-grained control over how clients interact with dSheltie.

Each key can be tied to specific IP addresses, allowed or forbidden RPC methods, and contract whitelists.
This is especially useful for multi-tenant environments or when you want to enforce stricter rules per application.

`key-management` fields:
* `id` - Unique identifier for the key. **_Required_**, **_Unique_**
* `type` - Defines the backend that manages this key. Currently supported: `local`. **_Required_**

if `type: local`, you mush provide:
```yaml
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

The `local` key type is the simplest form of key management. It allows you to define access keys directly in the configuration file, without relying on an external service. This is useful for quick setups and internal environments.

* `key` - The actual access key value. Any string. Should be passed via the `"X-Dsheltie-Key"` header. **_Required_**, **_Unique_**
* `settings.allowed-ips` - Restricts key usage to the listed IP addresses. When validating requests, dSheltie determines the client IPs in the following order:
  * It first checks the `X-Forwarded-For` header (commonly set by proxies or load balancers). If present, all comma-separated values are collected as candidate IPs
  * If the header is missing or empty, it falls back to the remote address of the TCP connection
  * If the remote address cannot be parsed, the request is assumed to come from `127.0.0.1`
* `settings.methods.allowed` - A whitelist of RPC methods that can be called with this key
* `settings.methods.forbidden` - A blacklist of RPC methods that cannot be called with this key
* `settings.contracts.allowed` - Restricts interaction to a specific set of contract addresses for `eth_call` and `eth_getLogs` methods

> **⚠️ Important**: If at least one key is defined in the `key-management` section, then every request must include a valid dSheltie key. If no key is provided, or the provided key does not match the configured rules, the request will be rejected.