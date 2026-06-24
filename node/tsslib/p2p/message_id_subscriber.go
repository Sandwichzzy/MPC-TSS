package p2p

import (
	"sync"
	"time"
)

type SubscriberDeliveryResult uint8

const (
	SubscriberDelivered SubscriberDeliveryResult = iota
	SubscriberNotFound
	SubscriberBusy
)

type MessageIDSubscriber struct {
	lock        *sync.RWMutex
	subscribers map[string]chan *Message
}

func NewMessageIDSubscriber() *MessageIDSubscriber {
	return &MessageIDSubscriber{
		lock:        &sync.RWMutex{},
		subscribers: make(map[string]chan *Message),
	}
}

// Subscribe a message id
func (ms *MessageIDSubscriber) Subscribe(msgID string, channel chan *Message) {
	ms.lock.Lock()
	defer ms.lock.Unlock()
	ms.subscribers[msgID] = channel
}

// UnSubscribe a messageid
func (ms *MessageIDSubscriber) UnSubscribe(msgID string) {
	ms.lock.Lock()
	defer ms.lock.Unlock()
	delete(ms.subscribers, msgID)
}

func (ms *MessageIDSubscriber) GetSubscriber(msgID string) chan *Message {
	ms.lock.RLock()
	defer ms.lock.RUnlock()
	c, ok := ms.subscribers[msgID]
	if !ok {
		return nil
	}
	return c
}

func (ms *MessageIDSubscriber) Deliver(msgID string, message *Message, timeout time.Duration) SubscriberDeliveryResult {
	ms.lock.RLock()
	defer ms.lock.RUnlock()

	channel, ok := ms.subscribers[msgID]
	if !ok {
		return SubscriberNotFound
	}

	if timeout <= 0 {
		select {
		case channel <- message:
			return SubscriberDelivered
		default:
			return SubscriberBusy
		}
	}

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case channel <- message:
		return SubscriberDelivered
	case <-timer.C:
		return SubscriberBusy
	}
}

func (ms *MessageIDSubscriber) IsEmpty() bool {
	ms.lock.RLock()
	defer ms.lock.RUnlock()
	return len(ms.subscribers) == 0
}
