package responder

import (
	"context"
	"fmt"
	"sync"

	config_proto "www.velocidex.com/golang/velociraptor/config/proto"
	crypto_proto "www.velocidex.com/golang/velociraptor/crypto/proto"
	"www.velocidex.com/golang/velociraptor/json"
	"www.velocidex.com/golang/velociraptor/logging"
)

// The pool event responder is a singleton which distributes any
// responses to all pool clients. It is used in order to initialize
// the pool client event table:

// 1. There is a singleton actions.EventTable object running a single
//    set of queries.
//
// 2. The global EventTable uses the global responder to forward event
//    result set.
//
// 3. The global responder multiplexes the same result set to all pool
//    clients.

// Therefore each event query result set will be duplicated to every
// pool client immediately.

var (
	mu                       sync.Mutex
	GlobalPoolEventResponder *PoolEventResponder
)

type PoolEventResponder struct {
	mu sync.Mutex

	ctx context.Context

	client_responders map[int]chan *crypto_proto.VeloMessage
}

func GetPoolEventResponder(ctx context.Context) *PoolEventResponder {
	mu.Lock()
	defer mu.Unlock()

	if GlobalPoolEventResponder != nil {
		return GlobalPoolEventResponder
	}

	result := &PoolEventResponder{
		ctx:               ctx,
		client_responders: make(map[int]chan *crypto_proto.VeloMessage),
	}

	GlobalPoolEventResponder = result
	return result
}

func (self *PoolEventResponder) RegisterPoolClientResponder(
	id int, outbound chan *crypto_proto.VeloMessage) {
	self.mu.Lock()
	defer self.mu.Unlock()

	self.client_responders[id] = outbound
}

// Gets a new responder which is feeding the GlobalPoolEventResponder
func (self *PoolEventResponder) NewResponder(
	config_obj *config_proto.Config,
	req *crypto_proto.VeloMessage) *Responder {
	// The PoolEventResponder input
	in := make(chan *crypto_proto.VeloMessage)

	// Prepare a new responder that will feed us.
	result := &Responder{
		request: req,
		output:  in,
		logger:  logging.GetLogger(config_obj, &logging.ClientComponent),
	}

	go func() {
		for {
			select {
			case <-self.ctx.Done():
				return
			case message, ok := <-in:
				if !ok {
					return
				}

				children := make([]chan *crypto_proto.VeloMessage, 0,
					len(self.client_responders))
				self.mu.Lock()
				for _, c := range self.client_responders {
					children = append(children, c)
				}
				self.mu.Unlock()

				fmt.Printf("Pushing message to %v listeners\n", len(children))
				json.Debug(message)
				for _, c := range children {
					select {
					case <-self.ctx.Done():
						return

					// Try to push the message if possible.
					case c <- message:
					default:
					}
				}
			}
		}
	}()

	return result
}
