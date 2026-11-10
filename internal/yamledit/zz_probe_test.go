package yamledit

import "testing"

func TestZZProbeFlowShapes(t *testing.T) {
	shapes := map[string]string{
		"doc shape (flow parent, flow entry)": "project_secrets: { bitbucket-token: { allow_data_to: [\"acme/app\"] } }\n",
		"block parent, flow entry":            "project_secrets:\n  bitbucket-token: { allow_data_to: [acme/app] }\n",
		"block parent, block entry, flow list": "project_secrets:\n  bitbucket-token:\n    allow_data_to: [acme/app]\n",
		"block parent, block entry, block list": "project_secrets:\n  bitbucket-token:\n    allow_data_to:\n      - acme/app\n",
	}
	ops := map[string]func([]byte) ([]byte, error){
		"RemoveListItem(list)": func(b []byte) ([]byte, error) {
			return RemoveListItem(b, []string{"project_secrets", "bitbucket-token", "allow_data_to"}, "acme/app")
		},
		"RemoveKey(list)": func(b []byte) ([]byte, error) {
			return RemoveKeyWithOptions(b, []string{"project_secrets", "bitbucket-token", "allow_data_to"}, RemoveOptions{})
		},
		"RemoveKey(entry,drop)": func(b []byte) ([]byte, error) {
			return RemoveKeyWithOptions(b, []string{"project_secrets", "bitbucket-token"}, RemoveOptions{DropEmptyParents: true})
		},
		"AddListItem(list)": func(b []byte) ([]byte, error) {
			return AddListItem(b, []string{"project_secrets", "bitbucket-token", "allow_data_to"}, "acme/web")
		},
		"SetScalar(proxy)": func(b []byte) ([]byte, error) {
			return SetScalar(b, []string{"project_secrets", "bitbucket-token", "proxy"}, "true")
		},
		// the two-step: normalize by rewriting the entry in block style, then splice
		"rewrite+Remove": func(b []byte) ([]byte, error) {
			n, err := RemoveKeyWithOptions(b, []string{"project_secrets", "bitbucket-token"}, RemoveOptions{DropEmptyParents: true})
			if err != nil {
				return nil, err
			}
			if n, err = AddListItem(n, []string{"project_secrets", "bitbucket-token", "allow_data_to"}, "acme/app"); err != nil {
				return nil, err
			}
			return RemoveListItem(n, []string{"project_secrets", "bitbucket-token", "allow_data_to"}, "acme/app")
		},
		"rewrite+RemoveKey": func(b []byte) ([]byte, error) {
			n, err := RemoveKeyWithOptions(b, []string{"project_secrets", "bitbucket-token"}, RemoveOptions{DropEmptyParents: true})
			if err != nil {
				return nil, err
			}
			if n, err = AddListItem(n, []string{"project_secrets", "bitbucket-token", "allow_data_to"}, "acme/app"); err != nil {
				return nil, err
			}
			return RemoveKeyWithOptions(n, []string{"project_secrets", "bitbucket-token", "allow_data_to"}, RemoveOptions{})
		},
	}
	for name, shape := range shapes {
		for opName, op := range ops {
			out, err := op([]byte(shape))
			t.Logf("%-38s %-20s err=%v out=%q", name, opName, err, string(out))
		}
	}
}
