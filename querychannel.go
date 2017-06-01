package main

import "sync"
import "gopkg.in/eapache/channels.v1"

type QueryChannel struct {
	sync.RWMutex
	channels []*channels.RingChannel
}

func NewQueryChannel() *QueryChannel {
	var channel QueryChannel
	return &channel
}

func (queryChannel *QueryChannel) Add() *channels.RingChannel {
	queryChannel.Lock()
	channel := channels.NewRingChannel(100)
	queryChannel.channels = append(queryChannel.channels, channel)
	queryChannel.Unlock()
	return channel
}

func (queryChannel *QueryChannel) Push(query Query) {
	for i := range queryChannel.channels {
		queryChannel.channels[i].In() <- query
	}
}
