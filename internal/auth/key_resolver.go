package auth

import (
	"errors"
	"fmt"
	"time"

	"github.com/drpcorg/nodecore/internal/config"
	"github.com/drpcorg/nodecore/internal/integration"
	"github.com/drpcorg/nodecore/internal/key_management"
	"github.com/drpcorg/nodecore/internal/protocol"
	"github.com/drpcorg/nodecore/pkg/utils"
	"github.com/rs/zerolog/log"
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
	retries := make([]func(), 0)
	initKeysDataArr := make([]*integration.InitKeysData, 0)

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

			initKeysData, err := integrationClient.InitKeys(keyCfg.DrpcKeyConfig)
			if err != nil {
				log.Warn().Err(err).Msgf("cound't init external %s keys with id '%s'", integrationClient.Type(), keyCfg.Id)
				var retryableErr *protocol.ClientRetryableError
				if errors.As(err, &retryableErr) {
					log.Warn().Msgf("init external %s keys with id '%s' will be retried", integrationClient.Type(), keyCfg.Id)
					retries = append(retries, resolver.retryInitKeys(keyCfg, integrationClient))
				}
			} else {
				log.Info().Msgf("extrenal %s keys with id '%s' have been successfully loaded", integrationClient.Type(), keyCfg.Id)
				initKeysDataArr = append(initKeysDataArr, initKeysData)
			}
		}
	}

	for _, initKeysData := range initKeysDataArr {
		go resolver.watchKeys(initKeysData)
	}
	for _, retry := range retries {
		go retry()
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

func (k *KeyResolver) retryInitKeys(keyCfg *config.KeyConfig, integration integration.IntegrationClient) func() {
	return func() {
		for {
			time.Sleep(k.retryInterval)
			initKeysData, err := integration.InitKeys(keyCfg.DrpcKeyConfig)
			if err != nil {
				log.Warn().Err(err).Msgf("cound't init external %s keys with id '%s'", integration.Type(), keyCfg.Id)
				continue
			}
			log.Info().Msgf("extrenal %s keys with id '%s' have been successfully loaded", integration.Type(), keyCfg.Id)
			go k.watchKeys(initKeysData)
			break
		}
	}
}

func (k *KeyResolver) watchKeys(data *integration.InitKeysData) {
	for _, initialKey := range data.InitialKeys {
		k.keys.Store(initialKey.GetKeyValue(), initialKey)
	}

	if data.KeyEvents == nil {
		return
	}

	for keyEvent := range data.KeyEvents {
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
