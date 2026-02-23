package local

import (
	"context"
	"fmt"
	"strings"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/samber/lo"
)

type LocalKey struct {
	id             string
	key            string
	keySettingsCfg *config.KeySettingsConfig
}

func (l *LocalKey) GetKeyValue() string {
	return l.key
}

func (l *LocalKey) Id() string {
	return l.id
}

func (l *LocalKey) PreCheckSetting(ctx context.Context) ([]string, error) {
	if l.keySettingsCfg == nil {
		return nil, nil
	}

	corsOrigins := l.keySettingsCfg.CorsOrigins

	if len(l.keySettingsCfg.AllowedIps) == 0 {
		return corsOrigins, nil
	}

	ips := utils.IpsFromContext(ctx)
	for _, allowedIp := range l.keySettingsCfg.AllowedIps {
		if ips.ContainsOne(allowedIp) {
			return corsOrigins, nil
		}
	}

	return corsOrigins, fmt.Errorf("ips [%s] are not allowed", strings.Join(ips.ToSlice(), ", "))
}

func (l *LocalKey) PostCheckSetting(_ context.Context, request protocol.RequestHolder) error {
	if l.keySettingsCfg == nil {
		return nil
	}

	methods := lo.Ternary(l.keySettingsCfg.Methods != nil, l.keySettingsCfg.Methods, &config.AuthMethods{})
	err := keydata.CheckMethod(methods.Allowed, methods.Forbidden, request.Method())
	if err != nil {
		return err
	}
	contracts := lo.Ternary(l.keySettingsCfg.AuthContracts != nil, l.keySettingsCfg.AuthContracts, &config.AuthContracts{})
	err = keydata.CheckContracts(contracts.Allowed, request)
	if err != nil {
		return err
	}

	return nil
}

func NewLocalKey(id string, keyCfg *config.LocalKeyConfig) *LocalKey {
	return &LocalKey{
		id:             id,
		key:            keyCfg.Key,
		keySettingsCfg: keyCfg.KeySettingsConfig,
	}
}

var _ keydata.Key = (*LocalKey)(nil)
