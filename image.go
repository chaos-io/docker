package docker

import (
	"bytes"
	"context"
	"crypto"
	"math/big"
	"os"
	"regexp"
	"strings"

	transformer "github.com/apenella/go-common-utils/transformer/string"
	"github.com/apenella/go-docker-builder/pkg/build"
	contextpath "github.com/apenella/go-docker-builder/pkg/build/context/path"
	"github.com/apenella/go-docker-builder/pkg/response"
)

func BuildImage(imagePath string, imageName string, imageTag string) error {
	w := bytes.NewBuffer(nil)

	res := response.NewDefaultResponse(
		response.WithTransformers(
			transformer.Prepend("buildPathContext"),
		),
		response.WithWriter(w),
	)

	dockerBuilder := build.NewDockerBuildCmd(cli).
		WithImageName(imageName).
		WithResponse(res)

	tag := imageName
	if len(imageTag) > 0 {
		tag = strings.Join([]string{imageName, imageTag}, ":")
	}
	dockerBuilder.AddTags(tag)
	dockerBuildContext := &contextpath.PathBuildContext{
		Path: imagePath,
	}

	if err := dockerBuilder.AddBuildContext(dockerBuildContext); err != nil {
		return err // errors.New("buildPathContext", "Error adding build docker context", err)
	}

	if err := dockerBuilder.Run(context.TODO()); err != nil {
		return err // errors.New("buildPathContext", fmt.Sprintf("Error building '%s'", imageName), err)
	}

	return nil
}

var loadImageRegexp = regexp.MustCompile(`(?m:^##load-image:.*$)`)

// ExtractLoadImage
//
// ##load-image:alpine
func ExtractLoadImage(buildFile string) string {
	content, err := os.ReadFile(buildFile)
	if err != nil {
		return ""
	}

	image := loadImageRegexp.Find(content)
	if len(image) > 0 {
		return strings.TrimSpace(strings.TrimPrefix(string(image), "##load-image:"))
	}
	return ""
}

var b26Alphabet = []byte("abcdefghijkmnopqrstuvwxyz_")

func base26Encode(input []byte) []byte {
	var result []byte
	x := big.NewInt(0).SetBytes(input)
	base := big.NewInt(int64(len(b26Alphabet)))
	zero := big.NewInt(0)
	mod := &big.Int{}
	for x.Cmp(zero) != 0 {
		x.DivMod(x, base, mod) // 对x取余数
		result = append(result, b26Alphabet[mod.Int64()])
	}
	return result
}

func GetImageName(prefix, id string) string {
	if len(id) > 0 {
		bytes := crypto.MD5.New().Sum([]byte(id))
		return prefix + "." + string(base26Encode(bytes[0:10]))
	}

	return ""
}
