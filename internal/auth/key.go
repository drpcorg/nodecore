package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/dsheltie/internal/config"
	"github.com/drpcorg/dsheltie/internal/protocol"
	"github.com/drpcorg/dsheltie/pkg/utils"
	"github.com/samber/lo"
)

type KeyResolver struct {
	keys map[string]Key
}

func NewKeyResolver(keyCfgs []*config.KeyConfig) *KeyResolver {
	keys := make(map[string]Key)
	for _, keyCfg := range keyCfgs {
		switch keyCfg.Type {
		case config.Local:
			localKey := NewLocalKey(keyCfg)
			keys[localKey.key] = localKey
		}
	}

	return &KeyResolver{
		keys: keys,
	}
}

func (k *KeyResolver) GetKey(keyStr string) (Key, bool) {
	key, ok := k.keys[keyStr]
	return key, ok
}

type Key interface {
	Id() string
	PreCheckSetting(ctx context.Context) error
	PostCheckSetting(ctx context.Context, request protocol.RequestHolder) error
}

type LocalKey struct {
	id             string
	key            string
	keySettingsCfg *config.KeySettingsConfig
}

func (l *LocalKey) Id() string {
	return l.id
}

func (l *LocalKey) PreCheckSetting(ctx context.Context) error {
	if len(l.keySettingsCfg.AllowedIps) == 0 {
		return nil
	}

	ips := utils.IpsFromContext(ctx)
	for _, allowedIp := range l.keySettingsCfg.AllowedIps {
		if ips.ContainsOne(allowedIp) {
			return nil
		}
	}

	return fmt.Errorf("ips [%s] are not allowed", strings.Join(ips.ToSlice(), ", "))
}

func (l *LocalKey) PostCheckSetting(_ context.Context, request protocol.RequestHolder) error {
	err := CheckMethod(l.keySettingsCfg.Methods, request.Method())
	if err != nil {
		return err
	}
	err = CheckContracts(l.keySettingsCfg.AuthContracts, request)
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

func CheckMethod(methodsCfg *config.AuthMethods, method string) error {
	if methodsCfg != nil {
		if len(methodsCfg.Allowed) > 0 {
			if !lo.Contains(methodsCfg.Allowed, method) {
				return fmt.Errorf("method '%s' is not allowed", method)
			}
		}
		if len(methodsCfg.Forbidden) > 0 {
			if lo.Contains(methodsCfg.Forbidden, method) {
				return fmt.Errorf("method '%s' is not allowed", method)
			}
		}
	}

	return nil
}

func CheckContracts(contractsCfg *config.AuthContracts, request protocol.RequestHolder) error {
	if contractsCfg != nil && len(contractsCfg.Allowed) > 0 {
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
			allowed := lo.Contains(contractsCfg.Allowed, toParam)
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
				allowed := lo.Contains(contractsCfg.Allowed, logAddress)
				if !allowed {
					return fmt.Errorf("'%s' address is not allowed", logAddress)
				}
			}
		}
	}
	return nil
}
