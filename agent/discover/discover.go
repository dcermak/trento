package discover

import (
	"fmt"
	"os"

	consulApi "github.com/hashicorp/consul/api"
	"github.com/trento-project/trento/internal/consul"
)

type Discovery func() (Discoverer, error)

type Discoverer interface {
	// Returns an arbitrary unique string identifier of the discovery, so that we can associate it to a Consul check ID
	GetId() string
	// this function checks whether this particular implementation of Discover
	// is relevant for this node. It is used as a gating condition for other
	// functionality in this implementation
	ShouldDiscover(client consul.Client) bool
	// Execute one iteration of a discovery and store the result in the Consul
	// KVStore.
	Discover() error

	// Create or Updating the given Consul Key-Value Path Store with a new value from the Agent
	storeDiscovery(cStorePath, cStoreValue string) error
}

type Discover struct {
	id     string
	client consul.Client
	host   string
}

func (discover Discover) GetId() string {
	return discover.id
}

// Create or Updating the given Consul Key-Value Path Store with a new value from the Agent
func (discover Discover) storeDiscovery(cStorePath, cStoreValue string) error {
	kvPath := fmt.Sprintf("%s/%s/%s", consul.KvHostsPath, discover.host, cStorePath)

	_, err := discover.client.KV().Put(&consulApi.KVPair{
		Key:   kvPath,
		Value: []byte(cStoreValue)}, nil)
	return err
}

// Execute one iteration of a discovery and store the result in the Consul KVStore.
func (discover Discover) Discover() error {
	discover.host, _ = os.Hostname()
	return nil
}

// Return a Host Discover instance
func NewDiscover(client consul.Client) Discover {
	r := Discover{}
	r.id = ""
	r.client = client
	r.host, _ = os.Hostname()
	return r
}