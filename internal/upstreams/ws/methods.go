package ws

type subscriptionMethod struct {
	Subscribe   string
	Unsubscribe string
}

var subscriptionMethods = []subscriptionMethod{
	{
		Subscribe:   "eth_subscribe",
		Unsubscribe: "eth_unsubscribe",
	},
	{
		Subscribe:   "chain_subscribeFinalizedHeads",
		Unsubscribe: "chain_unsubscribeFinalizedHeads",
	},
	{
		Subscribe:   "chain_subscribeFinalisedHeads",
		Unsubscribe: "chain_unsubscribeFinalisedHeads",
	},
	{
		Subscribe:   "subscribe_newHead",
		Unsubscribe: "unsubscribe_newHead",
	},
	{
		Subscribe:   "chain_subscribeNewHead",
		Unsubscribe: "chain_unsubscribeNewHead",
	},
	{
		Subscribe:   "chain_subscribeAllHeads",
		Unsubscribe: "chain_unsubscribeAllHeads",
	},
	{
		Subscribe:   "chain_subscribeNewHeads",
		Unsubscribe: "chain_unsubscribeNewHeads",
	},
	{
		Subscribe:   "chain_subscribeRuntimeVersion",
		Unsubscribe: "chain_unsubscribeRuntimeVersion",
	},
	{
		Subscribe:   "state_subscribeRuntimeVersion",
		Unsubscribe: "state_unsubscribeRuntimeVersion",
	},
	{
		Subscribe:   "author_submitAndWatchExtrinsic",
		Unsubscribe: "author_unwatchExtrinsic",
	},
	{
		Subscribe:   "grandpa_subscribeJustifications",
		Unsubscribe: "grandpa_unsubscribeJustifications",
	},
	{
		Subscribe:   "state_subscribeStorage",
		Unsubscribe: "state_unsubscribeStorage",
	},
	{
		Subscribe:   "transaction_unstable_submitAndWatch",
		Unsubscribe: "transaction_unstable_unwatch",
	},
	{
		Subscribe:   "accountSubscribe",
		Unsubscribe: "accountUnsubscribe",
	},
	{
		Subscribe:   "blockSubscribe",
		Unsubscribe: "blockUnsubscribe",
	},
	{
		Subscribe:   "logsSubscribe",
		Unsubscribe: "logsUnsubscribe",
	},
	{
		Subscribe:   "programSubscribe",
		Unsubscribe: "programUnsubscribe",
	},
	{
		Subscribe:   "signatureSubscribe",
		Unsubscribe: "signatureUnsubscribe",
	},
	{
		Subscribe:   "slotSubscribe",
		Unsubscribe: "slotUnsubscribe",
	},
}

var subMethods = make(map[string]string)

func init() {
	for _, method := range subscriptionMethods {
		subMethods[method.Subscribe] = method.Unsubscribe
	}
}

func IsSubscribeMethod(method string) bool {
	_, ok := GetUnsubscribeMethod(method)
	return ok
}

func GetUnsubscribeMethod(subMethod string) (string, bool) {
	method, ok := subMethods[subMethod]
	return method, ok
}
