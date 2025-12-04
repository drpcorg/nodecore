package integration

import keymanagement "github.com/drpcorg/nodecore/internal/key_management"

type InitKeysData struct {
	InitialKeys []keymanagement.Key
	KeyEvents   chan KeyEvent
}

func NewInitKeysData(keys []keymanagement.Key, eventChan chan KeyEvent) *InitKeysData {
	return &InitKeysData{
		InitialKeys: keys,
		KeyEvents:   eventChan,
	}
}

type KeyEvent interface {
	event()
}

type UpdatedKeyEvent struct {
	NewKey keymanagement.Key
}

func NewUpdatedKeyEvent(newKey keymanagement.Key) *UpdatedKeyEvent {
	return &UpdatedKeyEvent{
		NewKey: newKey,
	}
}

func (e *UpdatedKeyEvent) event() {}

type RemovedKeyEvent struct {
	RemovedKey keymanagement.Key
}

func NewRemovedKeyEvent(removedKey keymanagement.Key) *RemovedKeyEvent {
	return &RemovedKeyEvent{
		RemovedKey: removedKey,
	}
}

func (e *RemovedKeyEvent) event() {}
