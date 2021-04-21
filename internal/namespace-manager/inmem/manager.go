package inmem

import (
	"context"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strings"

	ac "github.com/jon-whit/zanzibar-poc/access-controller/internal"
	"github.com/xeipuuv/gojsonschema"
	"gopkg.in/yaml.v2"
)

const configExt = "yaml"

type inmemNamespaceManager struct {
	configs  map[string]*ac.NamespaceConfig
	rewrites map[string]*ac.RewriteRule
}

func NewNamespaceManager(path string) (ac.NamespaceManager, error) {

	configs := make(map[string]*ac.NamespaceConfig)
	rewrites := make(map[string]*ac.RewriteRule)

	if path == "" {
		return nil, fmt.Errorf("An empty path must not be provided")
	}

	// todo: grab this more dynamically
	schemaLoader := gojsonschema.NewReferenceLoader("file:///Users/Jonathan/github/jon-whit/zanzibar-poc/api/schemas.json/namespace-config.json")

	err := filepath.Walk(path, func(p string, info os.FileInfo, err error) error {
		if info.IsDir() {
			return nil
		}

		index := strings.LastIndex(p, ".")
		if index == -1 {
			return nil
		}

		ext := p[index+1:]
		if ext != configExt {
			return nil
		}

		blob, err := ioutil.ReadFile(p)
		if err != nil {
			return err
		}

		var config ac.NamespaceConfig
		err = yaml.Unmarshal(blob, &config)
		if err != nil {
			return err
		}

		documentLoader := gojsonschema.NewGoLoader(config)

		result, err := gojsonschema.Validate(schemaLoader, documentLoader)
		if err != nil {
			// todo: handle this error case better
			panic(err.Error())
		}

		if !result.Valid() {
			fmt.Printf("The document is not valid. see errors :\n")

			for _, desc := range result.Errors() {
				fmt.Printf("- %s\n", desc)
			}

			// todo: handle this error better with wrapped errors
			panic(fmt.Sprintf("The '%s' namespace config is not a valid namespace config specification", config.Name))
		}

		namespace := config.Name
		configs[namespace] = &config

		for _, relation := range config.Relations {
			var rule ac.RewriteRule

			if relation.UsersetRewrite != nil {
				rule = ac.ExpandRewriteOperand(relation.Name, relation.UsersetRewrite)
			} else {
				// A relation with no rewrites references itself
				rule = ac.RewriteRule{Operand: ac.ThisRelation{Relation: relation.Name}}
			}

			rewrites[namespace+"#"+relation.Name] = &rule
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	m := inmemNamespaceManager{
		configs,
		rewrites,
	}
	return &m, nil

}

func (i *inmemNamespaceManager) GetRewriteRule(ctx context.Context, namespace, relation string) (*ac.RewriteRule, error) {

	key := fmt.Sprintf("%s#%s", namespace, relation)
	rewrite, ok := i.rewrites[key]
	if ok {
		return rewrite, nil
	}

	return nil, nil
}

func (i *inmemNamespaceManager) GetNamespaceConfig(ctx context.Context, namespace string) (*ac.NamespaceConfig, error) {

	config, ok := i.configs[namespace]
	if ok {
		return config, nil
	}

	return nil, nil
}
