package state

import (
	bolt "go.etcd.io/bbolt"
	"google.golang.org/protobuf/proto"

	nodev1 "github.com/Arman2122/p-ui/api/node/v1"
)

func (s *Store) RetainedDeployments() ([]*nodev1.Deployment, error) {
	var deployments []*nodev1.Deployment
	err := s.db.View(func(tx *bolt.Tx) error {
		return tx.Bucket(deployBucket).ForEach(func(_, data []byte) error {
			if len(deployments) >= 2 {
				return ErrRevision
			}
			deployment := new(nodev1.Deployment)
			if err := proto.Unmarshal(data, deployment); err != nil {
				return err
			}
			deployments = append(deployments, deployment)
			return nil
		})
	})
	return deployments, err
}
