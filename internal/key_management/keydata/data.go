package keydata

import (
	"context"
	"errors"
	"fmt"
	"strings"

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
			if err := checkParamIsNotRepeated(body, "to"); err != nil {
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
			if err := checkParamIsNotRepeated(body, "address"); err != nil {
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
			// a node reads a null or empty 'address' as "every contract", and any
			// other type names no contract at all - with nothing to check against
			// the allowed list the request must not pass
			if len(addresses) == 0 {
				return errors.New("'address' param must be a string or a non-empty array of strings due to the contracts settings")
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

// RestrictUpstreams limits the request to upstreams carrying at least one of
// groupLabels, by adding a group-label selector the execution flow routes on.
// It fails closed: a request that cannot carry the selector is rejected
// rather than served by an unrestricted upstream.
func RestrictUpstreams(groupLabels []string, request protocol.RequestHolder) error {
	if len(groupLabels) == 0 {
		return nil
	}
	appender, ok := request.(protocol.SelectorAppender)
	if !ok {
		return fmt.Errorf("request of type %T cannot be restricted to the key's upstreams", request)
	}
	appender.AppendSelectors(protocol.RequestGroupLabelSelector{Labels: groupLabels})
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

// checkParamIsNotRepeated fails a request whose first param names the field
// more than once.
//
// CheckContracts reads the first key spelled exactly as name, but the params
// are forwarded as the client wrote them, and the node decides for itself
// which value it takes: a node that decodes with Go's encoding/json takes the
// last of repeated keys and matches their names case-insensitively. A second
// key is then a value that was never checked, so the request is turned away
// rather than guessed at. EqualFold is that same match - it folds the few
// non-ASCII letters encoding/json also reads as ASCII ones.
func checkParamIsNotRepeated(body []byte, name string) error {
	firstParam, err := sonic.Get(body, "params", 0)
	if err != nil {
		return err
	}
	matches := 0
	err = firstParam.ForEach(func(path ast.Sequence, _ *ast.Node) bool {
		if path.Key != nil && strings.EqualFold(*path.Key, name) {
			matches++
		}
		return matches < 2
	})
	if err != nil {
		return err
	}
	if matches > 1 {
		return fmt.Errorf("'%s' param must not be repeated due to the contracts settings", name)
	}
	return nil
}
