// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package common

import (
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
)

var _ handler.EventHandler = &EnqueueRequestsFromMapFunc{}

// EnqueueRequestsFromMapFunc same as original EnqueueRequestsFromMapFunc
// execept this doesn't queue old object for update
type EnqueueRequestsFromMapFunc struct {
	// Mapper transforms the argument into a slice of keys to be reconciled
	ToRequests handler.Mapper
}

// Create implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Create(evt event.CreateEvent, q workqueue.RateLimitingInterface) {
	e.mapAndEnqueue(q, handler.MapObject{Meta: evt.Meta, Object: evt.Object})
}

// Update implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Update(evt event.UpdateEvent, q workqueue.RateLimitingInterface) {
	// e.mapAndEnqueue(q, MapObject{Meta: evt.MetaOld, Object: evt.ObjectOld})
	e.mapAndEnqueue(q, handler.MapObject{Meta: evt.MetaNew, Object: evt.ObjectNew})
}

// Delete implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Delete(evt event.DeleteEvent, q workqueue.RateLimitingInterface) {
	e.mapAndEnqueue(q, handler.MapObject{Meta: evt.Meta, Object: evt.Object})
}

// Generic implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Generic(evt event.GenericEvent, q workqueue.RateLimitingInterface) {
	e.mapAndEnqueue(q, handler.MapObject{Meta: evt.Meta, Object: evt.Object})
}

func (e *EnqueueRequestsFromMapFunc) mapAndEnqueue(q workqueue.RateLimitingInterface, object handler.MapObject) {
	for _, req := range e.ToRequests.Map(object) {
		q.Add(req)
	}
}
