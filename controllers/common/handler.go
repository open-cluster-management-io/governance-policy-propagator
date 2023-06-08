// Copyright (c) 2021 Red Hat, Inc.
// Copyright Contributors to the Open Cluster Management project

package common

import (
	"context"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
)

var _ handler.EventHandler = &EnqueueRequestsFromMapFunc{}

// EnqueueRequestsFromMapFunc same as original EnqueueRequestsFromMapFunc
// execept this doesn't queue old object for update
type EnqueueRequestsFromMapFunc struct {
	// Mapper transforms the argument into a slice of keys to be reconciled
	ToRequests handler.MapFunc
}

// Create implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Create(ctx context.Context, evt event.CreateEvent,
	q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Update implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Update(ctx context.Context, evt event.UpdateEvent,
	q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.ObjectNew)
}

// Delete implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Delete(ctx context.Context, evt event.DeleteEvent,
	q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

// Generic implements EventHandler
func (e *EnqueueRequestsFromMapFunc) Generic(ctx context.Context, evt event.GenericEvent,
	q workqueue.RateLimitingInterface,
) {
	e.mapAndEnqueue(ctx, q, evt.Object)
}

func (e *EnqueueRequestsFromMapFunc) mapAndEnqueue(ctx context.Context, q workqueue.RateLimitingInterface,
	object client.Object,
) {
	for _, req := range e.ToRequests(ctx, object) {
		q.Add(req)
	}
}

var NeverEnqueue = predicate.NewPredicateFuncs(func(o client.Object) bool { return false })
