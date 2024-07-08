package docker

import (
	"context"
	"fmt"
	"regexp"
	"time"

	"github.com/chaos-io/chaos/logs"
	"github.com/docker/docker/api/types"
	"github.com/docker/docker/client"
)

var demoImageName = regexp.MustCompile(`demo-([\w\-]+)?\.([a-z\d]+)?`)

type ImageCleaner struct {
	Client               *client.Client
	InvalidDuration      time.Duration
	ForceRemoveContainer bool
}

func NewImageCleaner(invalidDuration time.Duration) *ImageCleaner {
	return &ImageCleaner{
		InvalidDuration:      invalidDuration,
		ForceRemoveContainer: true,
	}
}

func (i *ImageCleaner) Clean() error {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithHost(Host), client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("failed to connect docker, %w", err)
	}
	defer c.Close()
	i.Client = c

	images, err := c.ImageList(context.Background(), types.ImageListOptions{All: true})
	if err != nil {
		return fmt.Errorf("failed to list the docker images, %w", err)
	}

	for _, image := range images {
		imageCreated := time.Unix(image.Created, 0)
		if len(image.RepoTags) == 1 && demoImageName.MatchString(image.RepoTags[0]) && i.imageNeedRemove(image.ID, imageCreated) {
			resItem, err := c.ImageRemove(context.Background(), image.ID, types.ImageRemoveOptions{Force: true, PruneChildren: true})
			if err != nil {
				logs.Warnw("failed to remove the image", "id", image.ID, "name", image.RepoTags[0], "error", err)
			} else {
				logs.Infow(" removed the image", "id", image.ID, "name", image.RepoTags[0], "created", imageCreated)
				for i, value := range resItem {
					logs.Infow(" removed the image response item", "no", i, "deleted", value.Deleted, "untagged", value.Untagged)
				}
			}
		}
	}
	return nil
}

func (i *ImageCleaner) imageNeedRemove(imageId string, created time.Time) bool {
	duration := time.Now().Sub(created)
	if duration < i.InvalidDuration {
		return false
	}

	return !i.hasValidContainers(imageId)
}

func (i *ImageCleaner) hasValidContainers(imageId string) bool {
	containers, err := i.Client.ContainerList(context.Background(), types.ContainerListOptions{All: true})
	if err != nil {
		logs.Warnw("failed to list the docker containers", "imageId", imageId, "error", err)
		return true
	}

	var removingContainers []string
	for _, container := range containers {
		if container.ImageID != imageId {
			continue
		}

		if container.State == "running" {
			return true
		}

		created := time.Unix(container.Created, 0)
		if time.Now().Sub(created) > i.InvalidDuration {
			removingContainers = append(removingContainers, container.ID)
		} else {
			return true
		}
	}

	if i.ForceRemoveContainer {
		allRemoved := true
		for _, id := range removingContainers {
			if err = Remove(id); err != nil {
				allRemoved = false
				logs.Warnw("failed to remove the container", "id", id, "error", err)
			}
		}
		if !allRemoved {
			return true
		}
	}

	return false
}
