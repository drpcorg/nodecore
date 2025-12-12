package keymanagement

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/samber/lo"
)

type Key interface {
	Id() string
	GetKeyValue() string
	PreCheckSetting(ctx context.Context) ([]string, error)
	PostCheckSetting(ctx context.Context, request protocol.RequestHolder) error
}

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
	err := CheckMethod(methods.Allowed, methods.Forbidden, request.Method())
	if err != nil {
		return err
	}
	contracts := lo.Ternary(l.keySettingsCfg.AuthContracts != nil, l.keySettingsCfg.AuthContracts, &config.AuthContracts{})
	err = CheckContracts(contracts.Allowed, request)
	if err != nil {
		return err
	}

	return nil
}

func NewLocalKey(keyCfg *config.KeyConfig) *LocalKey {
	return &LocalKey{
		id:             keyCfg.Id,
		key:            keyCfg.LocalKeyConfig.Key,
		keySettingsCfg: keyCfg.LocalKeyConfig.KeySettingsConfig,
	}
}

var _ Key = (*LocalKey)(nil)

func CheckMethod(allowedMethods, forbiddenMethods []string, method string) error {
	if len(allowedMethods) > 0 {
		if !lo.Contains(allowedMethods, method) {
			return fmt.Errorf("method '%s' is not allowed", method)
		}
	}
	if len(forbiddenMethods) > 0 {
		if lo.Contains(forbiddenMethods, method) {
			return fmt.Errorf("method '%s' is not allowed", method)
		}
	}

	return nil
}

func CheckContracts(contracts []string, request protocol.RequestHolder) error {
	if len(contracts) > 0 {
		switch request.Method() {
		case "eth_call":
			body, err := request.Body()
			if err != nil {
				return err
			}
			toParamNode, err := sonic.Get(body, "params", 0, "to")
			if err != nil {
				if !toParamNode.Exists() {
					return errors.New("'to' param is mandatory due to the contracts settings")
				}
				return err
			}
			if toParamNode.TypeSafe() != ast.V_STRING {
				return errors.New("'to' param must be string")
			}
			toParam, _ := toParamNode.String()
			allowed := lo.Contains(contracts, toParam)
			if !allowed {
				return fmt.Errorf("'%s' address is not allowed", toParam)
			}
		case "eth_getLogs":
			body, err := request.Body()
			if err != nil {
				return err
			}
			addressParamNode, err := sonic.Get(body, "params", 0, "address")
			if err != nil {
				if !addressParamNode.Exists() {
					return errors.New("'address' param is mandatory due to the contracts settings")
				}
				return err
			}
			addresses := make([]string, 0)
			switch addressParamNode.TypeSafe() {
			case ast.V_ARRAY:
				addressParam, _ := addressParamNode.Array()
				for _, address := range addressParam {
					addressStr, ok := address.(string)
					if !ok {
						return errors.New("value in 'address' param must be string")
					}
					addresses = append(addresses, addressStr)
				}
			case ast.V_STRING:
				address, _ := addressParamNode.String()
				addresses = append(addresses, address)
			}
			for _, logAddress := range addresses {
				allowed := lo.Contains(contracts, logAddress)
				if !allowed {
					return fmt.Errorf("'%s' address is not allowed", logAddress)
				}
			}
		}
	}
	return nil
}
