package drpc

import (
	"context"
	"fmt"
	"strings"

	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
)

type DrpcKey struct {
	KeyId             string   `json:"key_id"`
	IpWhitelist       []string `json:"ip_whitelist"`
	MethodsBlacklist  []string `json:"methods_blacklist"`
	MethodsWhitelist  []string `json:"methods_whitelist"`
	ContractWhitelist []string `json:"contract_whitelist"`
	CorsOrigins       []string `json:"cors_origins"`
	ApiKey            string   `json:"api_key"`
}

func (d *DrpcKey) Id() string {
	return d.KeyId
}

func (d *DrpcKey) GetKeyValue() string {
	return d.ApiKey
}

func (d *DrpcKey) PreCheckSetting(ctx context.Context) ([]string, error) {
	corsOrigins := d.CorsOrigins

	if len(d.IpWhitelist) == 0 {
		return corsOrigins, nil
	}

	ips := utils.IpsFromContext(ctx)
	for _, allowedIp := range d.IpWhitelist {
		if ips.ContainsOne(allowedIp) {
			return corsOrigins, nil
		}
	}

	return corsOrigins, fmt.Errorf("ips [%s] are not allowed", strings.Join(ips.ToSlice(), ", "))
}

func (d *DrpcKey) PostCheckSetting(_ context.Context, request protocol.RequestHolder) error {
	err := keydata.CheckMethod(d.MethodsWhitelist, d.MethodsBlacklist, request.Method())
	if err != nil {
		return err
	}
	err = keydata.CheckContracts(d.ContractWhitelist, request)
	if err != nil {
		return err
	}

	return nil
}

var _ keydata.Key = (*DrpcKey)(nil)
