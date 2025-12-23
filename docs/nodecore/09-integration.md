# Integration config guide

Nodecore supports integration with external platforms that provide a wide range of features. The `integration` section helps you set all of this up.

```yaml
integration:
  drpc:
    url: http://localhost:9090
    request-timeout: 35s
```

## DRPC integration

DRPC integration allows nodecore to fetch and maintain authentication keys that are managed in the DRPC platform instead of being defined locally. This enables centralized key management across multiple nodecore instances and unlocks future analytics features provided by DRPC.

* `url` - The DRPC integration endpoint. **_Required_**
* `request-timeout` - Timeout for communication with the DRPC integration API. **_Default_**: `10s`

To enable NodeCore ↔ DRPC integration, you must complete the Quickstart guide on the DRPC website:

1. Visit https://drpc.org/.
2. Sign in or create a new account.
3. Select the Nodecore product.
4. Complete the Quickstart guide for Nodecore.
5. Generate your owner's API token and get the integration endpoint.
6. Copy this endpoint into your nodecore configuration file.

### Current DRPC Features

1. DRPC-managed API keys — create and maintain nodecore keys directly in DRPC, and have nodecore automatically fetch them and enforce all associated restrictions (see [DRPC Key Management](03-auth.md#drpc-keys))

**Future updates on the DRPC side will introduce:**

1. Request stats (request counts, latency, error rate, etc).
2. Error stats.
3. Tracing stats.