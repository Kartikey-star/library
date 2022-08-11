package devfile

import (
	v1 "github.com/devfile/api/v2/pkg/apis/workspaces/v1alpha2"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/devfile/api/v2/pkg/validation/variables"
	"github.com/devfile/library/pkg/devfile/parser"
	"github.com/devfile/library/pkg/devfile/parser/data/v2/common"
)

func TestParseDevfileAndValidate(t *testing.T) {
	KubeComponentOriginalURIKey := "devfile.io/kubeComponent-originalURI"
	outerloopDeployContent := `
kind: Deployment
apiVersion: apps/v1
metadata:
  name: my-python
spec:
  replicas: 1
  selector:
    matchLabels:
      app: python-app
  template:
    metadata:
      labels:
        app: python-app
    spec:
      containers:
        - name: my-python
          image: my-python-image:{{ PARAMS }}
          ports:
            - name: http
              containerPort: 8081
              protocol: TCP
          resources:
            limits:
              memory: "128Mi"
              cpu: "500m"
`
	uri := "127.0.0.1:8080"
	var testServer *httptest.Server
	testServer = httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte(outerloopDeployContent))
		if err != nil {
			t.Errorf("unexpected error while writing outerloop-deploy.yaml: %v", err)
		}
	}))
	// create a listener with the desired port.
	l, err := net.Listen("tcp", uri)
	if err != nil {
		t.Errorf("Test_parseParentAndPluginFromURI() unexpected error while creating listener: %v", err)
	}

	// NewUnstartedServer creates a listener. Close that listener and replace
	// with the one we created.
	testServer.Listener.Close()
	testServer.Listener = l

	testServer.Start()
	defer testServer.Close()

	devfileContent := `commands:
- exec:
    commandLine: ./main {{ PARAMS }}
    component: runtime
    group:
      isDefault: true
      kind: run
    workingDir: ${PROJECT_SOURCE}
  id: run
components:
- container:
    endpoints:
    - name: http
      targetPort: 8080
    image: golang:latest
    memoryLimit: 1024Mi
    mountSources: true
  name: runtime
- kubernetes:
    uri: http://127.0.0.1:8080/outerloop-deploy.yaml
  name: outerloop-deploy
metadata:
  description: Stack with the latest Go version
  displayName: Go Runtime
  icon: https://raw.githubusercontent.com/devfile-samples/devfile-stack-icons/main/golang.svg
  language: go
  name: my-go-app
  projectType: go
  tags:
  - Go
  version: 1.0.0
schemaVersion: 2.2.0
`

	devfileContentWithVariable := devfileContent + `variables:
  PARAMS: foo`
	type args struct {
		args parser.ParserArgs
	}
	tests := []struct {
		name                 string
		args                 args
		wantVarWarning       variables.VariableWarning
		wantCommandLine      string
		wantKubernetesInline string
		wantVariables        map[string]string
	}{
		{
			name: "with external overriding variables",
			args: args{
				args: parser.ParserArgs{
					ExternalVariables: map[string]string{
						"PARAMS": "bar",
					},
					Data: []byte(devfileContentWithVariable),
				},
			},
			wantKubernetesInline: "image: my-python-image:bar",
			wantCommandLine:      "./main bar",
			wantVariables: map[string]string{
				"PARAMS": "bar",
			},
			wantVarWarning: variables.VariableWarning{
				Commands:        map[string][]string{},
				Components:      map[string][]string{},
				Projects:        map[string][]string{},
				StarterProjects: map[string][]string{},
			},
		},
		{
			name: "with new external variables",
			args: args{
				args: parser.ParserArgs{
					ExternalVariables: map[string]string{
						"OTHER": "other",
					},
					Data: []byte(devfileContentWithVariable),
				},
			},
			wantKubernetesInline: "image: my-python-image:foo",
			wantCommandLine:      "./main foo",
			wantVariables: map[string]string{
				"PARAMS": "foo",
				"OTHER":  "other",
			},
			wantVarWarning: variables.VariableWarning{
				Commands:        map[string][]string{},
				Components:      map[string][]string{},
				Projects:        map[string][]string{},
				StarterProjects: map[string][]string{},
			},
		}, {
			name: "with new external variables",
			args: args{
				args: parser.ParserArgs{
					ExternalVariables: map[string]string{
						"PARAMS": "baz",
					},
					Data: []byte(devfileContent),
				},
			},
			wantKubernetesInline: "image: my-python-image:baz",
			wantCommandLine:      "./main baz",
			wantVariables: map[string]string{
				"PARAMS": "baz",
			},
			wantVarWarning: variables.VariableWarning{
				Commands:        map[string][]string{},
				Components:      map[string][]string{},
				Projects:        map[string][]string{},
				StarterProjects: map[string][]string{},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotD, gotVarWarning, err := ParseDevfileAndValidate(tt.args.args)
			if err != nil {
				t.Errorf("ParseDevfileAndValidate() error = %v, wantErr nil", err)
				return
			}
			commands, err := gotD.Data.GetCommands(common.DevfileOptions{})
			if err != nil {
				t.Errorf("unexpected error getting commands")
			}
			expectedCommandLine := commands[0].Exec.CommandLine
			if expectedCommandLine != tt.wantCommandLine {
				t.Errorf("command line is %q, should be %q", expectedCommandLine, tt.wantCommandLine)
			}
			getKubeCompOptions := common.DevfileOptions{
				ComponentOptions: common.ComponentOptions{
					ComponentType: v1.KubernetesComponentType,
				},
			}
			kubeComponents, err := gotD.Data.GetComponents(getKubeCompOptions)
			if err != nil {
				t.Errorf("unexpected error getting kubernetes component")
			}
			kubenetesComponent := kubeComponents[0]
			if kubenetesComponent.Kubernetes.Uri != "" || kubenetesComponent.Kubernetes.Inlined == "" ||
				!strings.Contains(kubenetesComponent.Kubernetes.Inlined, tt.wantKubernetesInline) {
				t.Errorf("unexpected kubenetes component inlined, got %s, want include %s", kubenetesComponent.Kubernetes.Inlined, tt.wantKubernetesInline)
			}

			if kubenetesComponent.Attributes != nil {
				if originalUri := kubenetesComponent.Attributes.GetString(KubeComponentOriginalURIKey, &err); err != nil || originalUri != "http://127.0.0.1:8080/outerloop-deploy.yaml" {
					t.Errorf("ParseDevfileAndValidate() should set kubenetesComponent.Attributes, '%s', expected http://127.0.0.1:8080/outerloop-deploy.yaml, got %s",
						KubeComponentOriginalURIKey, originalUri)
				}
			} else {
				t.Error("ParseDevfileAndValidate() should set kubenetesComponent.Attributes, but got empty Attributes")
			}

			if !reflect.DeepEqual(gotVarWarning, tt.wantVarWarning) {
				t.Errorf("ParseDevfileAndValidate() gotVarWarning = %v, want %v", gotVarWarning, tt.wantVarWarning)
			}
			variables := gotD.Data.GetDevfileWorkspaceSpec().Variables
			if !reflect.DeepEqual(variables, tt.wantVariables) {
				t.Errorf("variables are %+v, expected %+v", variables, tt.wantVariables)
			}
		})
	}
}
