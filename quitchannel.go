package main

import "sync"

type QuitChannel struct {
	sync.RWMutex
	channels []chan bool
}

func NewQuitChannel() *QuitChannel {
	var channel QuitChannel
	return &channel
}

func (quitChannel *QuitChannel) Add() chan bool {
	quitChannel.Lock()
	channel := make(chan bool, 2)
	quitChannel.channels = append(quitChannel.channels, channel)
	quitChannel.Unlock()
	return channel
}

func (quitChannel *QuitChannel) SignalQuit() {
	for i := range quitChannel.channels {
		quitChannel.channels[i] <- true
	}
}

func (quitChannel *QuitChannel) WaitForCleanup() {
	for i := range quitChannel.channels {
		<-quitChannel.channels[i]
	}
}
