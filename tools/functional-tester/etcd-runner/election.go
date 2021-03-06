// Copyright 2016 The etcd Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"fmt"

	"github.com/coreos/etcd/clientv3/concurrency"
)

func runElection(getClient getClientFunc, rounds int) {
	rcs := make([]roundClient, 15)
	validatec, releasec := make(chan struct{}, len(rcs)), make(chan struct{}, len(rcs))
	for range rcs {
		releasec <- struct{}{}
	}

	for i := range rcs {
		v := fmt.Sprintf("%d", i)
		observedLeader := ""
		validateWaiters := 0

		rcs[i].c = getClient()
		var (
			s   *concurrency.Session
			err error
		)
		for {
			s, err = concurrency.NewSession(rcs[i].c)
			if err == nil {
				break
			}
		}
		e := concurrency.NewElection(s, "electors")

		rcs[i].acquire = func() error {
			<-releasec
			ctx, cancel := context.WithCancel(context.Background())
			go func() {
				if ol, ok := <-e.Observe(ctx); ok {
					observedLeader = string(ol.Kvs[0].Value)
					if observedLeader != v {
						cancel()
					}
				}
			}()
			err = e.Campaign(ctx, v)
			if err == nil {
				observedLeader = v
			}
			if observedLeader == v {
				validateWaiters = len(rcs)
			}
			select {
			case <-ctx.Done():
				return nil
			default:
				cancel()
				return err
			}
		}
		rcs[i].validate = func() error {
			if l, err := e.Leader(context.TODO()); err == nil && l != observedLeader {
				return fmt.Errorf("expected leader %q, got %q", observedLeader, l)
			}
			validatec <- struct{}{}
			return nil
		}
		rcs[i].release = func() error {
			for validateWaiters > 0 {
				select {
				case <-validatec:
					validateWaiters--
				default:
					return fmt.Errorf("waiting on followers")
				}
			}
			if err := e.Resign(context.TODO()); err != nil {
				return err
			}
			if observedLeader == v {
				for range rcs {
					releasec <- struct{}{}
				}
			}
			observedLeader = ""
			return nil
		}
	}

	doRounds(rcs, rounds)
}
