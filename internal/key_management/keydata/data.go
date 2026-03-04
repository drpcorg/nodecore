package keydata

import (
	"context"
	"errors"
	"fmt"

	"github.com/bytedance/sonic"
	"github.com/bytedance/sonic/ast"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/samber/lo"
)

type Key interface {
	Id() string
	GetKeyValue() string
	PreCheckSetting(ctx context.Context) ([]string, error)
	PostCheckSetting(ctx context.Context, request protocol.RequestHolder) error
}

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

type KeyEvent interface {
	event()
}

type UpdatedKeyEvent struct {
	NewKey Key
}

func NewUpdatedKeyEvent(newKey Key) *UpdatedKeyEvent {
	return &UpdatedKeyEvent{
		NewKey: newKey,
	}
}

func (e *UpdatedKeyEvent) event() {}

type RemovedKeyEvent struct {
	RemovedKey Key
}

func NewRemovedKeyEvent(removedKey Key) *RemovedKeyEvent {
	return &RemovedKeyEvent{
		RemovedKey: removedKey,
	}
}

func (e *RemovedKeyEvent) event() {}
