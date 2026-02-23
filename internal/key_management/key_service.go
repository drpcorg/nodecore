package keymanagement

import (
	"context"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/key_management/keydata"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type KeyService struct {
	ctx           context.Context
	keys          *utils.CMap[string, keydata.Key]
	retryInterval time.Duration
}

func NewKeyServiceWithRetryInterval(
	ctx context.Context,
	keyCfgs []*config.KeyConfig,
	integrationResolver *integration.IntegrationResolver,
	retryInterval time.Duration,
) (*KeyService, error) {
	resolver := &KeyService{
		retryInterval: retryInterval,
		keys:          utils.NewCMap[string, keydata.Key](),
		ctx:           ctx,
	}
	keyEventsChans := make([]<-chan keydata.KeyEvent, 0)

	for _, keyCfg := range keyCfgs {
		integrationType := integration.GetIntegrationType(keyCfg.Type)
		integrationClient, err := getIntegration(integrationResolver, integrationType)
		if err != nil {
			return nil, err
		}
		keyEvents, err := integrationClient.InitKeys(keyCfg.Id, keyCfg.GetSpecificKeyConfig())
		if err != nil {
			log.Warn().Err(err).Msgf("cound't init external %s keys with id '%s'", integrationClient.Type(), keyCfg.Id)
		} else {
			if keyEvents != nil {
				log.Info().Msgf("%s keys with id '%s' will be processed", integrationClient.Type(), keyCfg.Id)
				keyEventsChans = append(keyEventsChans, keyEvents)
			}
		}
	}

	if len(keyEventsChans) > 0 {
		allEvents := lo.FanIn(100, keyEventsChans...)
		go resolver.watchKeys(allEvents)
	}

	return resolver, nil
}

func NewKeyService(ctx context.Context, keyCfgs []*config.KeyConfig, integrationResolver *integration.IntegrationResolver) (*KeyService, error) {
	return NewKeyServiceWithRetryInterval(ctx, keyCfgs, integrationResolver, 10*time.Second)
}

func (k *KeyService) GetKey(keyStr string) (keydata.Key, bool) {
	key, ok := k.keys.Load(keyStr)
	return key, ok
}

func getIntegration(
	resolver *integration.IntegrationResolver,
	integrationType integration.IntegrationType,
) (integration.IntegrationClient, error) {
	integrationClient := resolver.GetIntegration(integrationType)
	if integrationClient == nil {
		return nil, fmt.Errorf("there is no %s integration config to load %s keys", integrationType, integrationType)
	}
	return integrationClient, nil
}

func (k *KeyService) watchKeys(keyEvents <-chan keydata.KeyEvent) {
	for {
		select {
		case <-k.ctx.Done():
			return
		case keyEvent, ok := <-keyEvents:
			if ok {
				switch ev := keyEvent.(type) {
				case *keydata.UpdatedKeyEvent:
					log.Info().Msgf("key '%s' has been updated'", ev.NewKey.Id())
					k.keys.Store(ev.NewKey.GetKeyValue(), ev.NewKey)
				case *keydata.RemovedKeyEvent:
					log.Info().Msgf("key '%s' has been removed or deactivated", ev.RemovedKey.Id())
					k.keys.Delete(ev.RemovedKey.GetKeyValue())
				}
			}
		}
	}
}
