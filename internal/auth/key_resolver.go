package auth

import (
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/key_management"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
	"github.com/samber/lo"
)

type KeyResolver struct {
	keys          *utils.CMap[string, keymanagement.Key]
	retryInterval time.Duration
}

func NewKeyResolverWithRetryInterval(
	keyCfgs []*config.KeyConfig,
	integrationResolver *integration.IntegrationResolver,
	retryInterval time.Duration,
) (*KeyResolver, error) {
	keys := utils.NewCMap[string, keymanagement.Key]()
	resolver := &KeyResolver{
		retryInterval: retryInterval,
		keys:          keys,
	}
	keyEventsChans := make([]<-chan integration.KeyEvent, 0)

	for _, keyCfg := range keyCfgs {
		switch keyCfg.Type {
		case config.LocalKey:
			localKey := keymanagement.NewLocalKey(keyCfg)
			keys.Store(localKey.GetKeyValue(), localKey)
		case config.DrpcKey:
			integrationClient, err := getIntegration(integrationResolver, integration.Drpc)
			if err != nil {
				return nil, err
			}

			keyEvents, err := integrationClient.InitKeys(keyCfg.DrpcKeyConfig)
			if err != nil {
				log.Warn().Err(err).Msgf("cound't init external %s keys with id '%s'", integrationClient.Type(), keyCfg.Id)
			} else {
				if keyEvents != nil {
					log.Info().Msgf("extrenal %s keys with id '%s' will be processed", integrationClient.Type(), keyCfg.Id)
					keyEventsChans = append(keyEventsChans, keyEvents)
				}
			}
		}
	}

	if len(keyEventsChans) > 0 {
		allEvents := lo.FanIn(100, keyEventsChans...)
		go resolver.watchKeys(allEvents)
	}

	return resolver, nil
}

func NewKeyResolver(keyCfgs []*config.KeyConfig, integrationResolver *integration.IntegrationResolver) (*KeyResolver, error) {
	return NewKeyResolverWithRetryInterval(keyCfgs, integrationResolver, 10*time.Second)
}

func (k *KeyResolver) GetKey(keyStr string) (keymanagement.Key, bool) {
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

func (k *KeyResolver) watchKeys(keyEvents <-chan integration.KeyEvent) {
	for keyEvent := range keyEvents {
		switch ev := keyEvent.(type) {
		case *integration.UpdatedKeyEvent:
			log.Info().Msgf("key '%s' has been updated'", ev.NewKey.Id())
			k.keys.Store(ev.NewKey.GetKeyValue(), ev.NewKey)
		case *integration.RemovedKeyEvent:
			log.Info().Msgf("key '%s' has been removed or deactivated", ev.RemovedKey.Id())
			k.keys.Delete(ev.RemovedKey.GetKeyValue())
		}
	}
}
