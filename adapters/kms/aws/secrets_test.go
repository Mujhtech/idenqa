package aws

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Mujhtech/idenqa/internal/platform/secret"
	"github.com/aws/aws-sdk-go-v2/aws"
	awssm "github.com/aws/aws-sdk-go-v2/service/secretsmanager"
)

type fakeSecretsClient struct {
	output *awssm.GetSecretValueOutput
	err    error
	inputs []*awssm.GetSecretValueInput
}

func (client *fakeSecretsClient) GetSecretValue(_ context.Context, input *awssm.GetSecretValueInput, _ ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error) {
	client.inputs = append(client.inputs, input)
	if client.err != nil {
		return nil, client.err
	}
	if client.output != nil {
		return client.output, nil
	}
	return &awssm.GetSecretValueOutput{}, nil
}

func mustReference(t *testing.T, value string) secret.Reference {
	t.Helper()
	reference, err := secret.ParseReference(value)
	if err != nil {
		t.Fatalf("ParseReference(%q) error = %v", value, err)
	}
	return reference
}

func TestSecretsResolvesStringBinaryAndVersionSelectors(t *testing.T) {
	t.Parallel()

	const versionID = "1234abcd-12ab-34cd-56ef-1234567890ab"
	client := &fakeSecretsClient{}
	outputs := []*awssm.GetSecretValueOutput{
		{SecretString: aws.String("idq_wrk_v1_credential"), VersionId: aws.String(versionID)},
		{SecretBinary: []byte{0x01, 0x02, 0x03}, VersionId: aws.String(versionID)},
		{SecretString: aws.String("staged"), VersionId: aws.String(versionID)},
		{SecretString: aws.String("exact"), VersionId: aws.String(versionID)},
	}
	resolver, err := NewSecrets(&sequencedSecretsClient{outputs: outputs, inputs: &client.inputs})
	if err != nil {
		t.Fatalf("NewSecrets() error = %v", err)
	}

	stringValue, err := resolver.Resolve(context.Background(), mustReference(t, "secret://aws/prod/idenqa/runner"))
	if err != nil {
		t.Fatalf("Resolve(string) error = %v", err)
	}
	text, err := stringValue.Text()
	if err != nil || text != "idq_wrk_v1_credential" || stringValue.Version() != versionID {
		t.Fatalf("Resolve(string) = %q version=%q error=%v", text, stringValue.Version(), err)
	}

	binaryValue, err := resolver.Resolve(context.Background(), mustReference(t, "secret://aws/prod/idenqa/binary"))
	if err != nil || len(binaryValue.Data()) != 3 {
		t.Fatalf("Resolve(binary) = %v, %v", binaryValue.Data(), err)
	}

	if _, err := resolver.Resolve(context.Background(), mustReference(t, "secret://aws/prod/idenqa/staged?version=AWSCURRENT")); err != nil {
		t.Fatalf("Resolve(stage) error = %v", err)
	}
	exact, err := resolver.Resolve(context.Background(), mustReference(t, "secret://aws/prod/idenqa/runner?version="+versionID))
	if err != nil || exact.Version() != versionID {
		t.Fatalf("Resolve(version id) error = %v", err)
	}

	if client.inputs[0].VersionId != nil || client.inputs[0].VersionStage != nil {
		t.Fatalf("unversioned input = %+v", client.inputs[0])
	}
	if aws.ToString(client.inputs[0].SecretId) != "prod/idenqa/runner" {
		t.Fatalf("SecretId = %q", aws.ToString(client.inputs[0].SecretId))
	}
	if aws.ToString(client.inputs[2].VersionStage) != "AWSCURRENT" || client.inputs[2].VersionId != nil {
		t.Fatalf("stage input = %+v", client.inputs[2])
	}
	if aws.ToString(client.inputs[3].VersionId) != versionID || client.inputs[3].VersionStage != nil {
		t.Fatalf("version input = %+v", client.inputs[3])
	}
}

type sequencedSecretsClient struct {
	outputs []*awssm.GetSecretValueOutput
	inputs  *[]*awssm.GetSecretValueInput
}

func (client *sequencedSecretsClient) GetSecretValue(_ context.Context, input *awssm.GetSecretValueInput, _ ...func(*awssm.Options)) (*awssm.GetSecretValueOutput, error) {
	*client.inputs = append(*client.inputs, input)
	index := len(*client.inputs) - 1
	if index < len(client.outputs) {
		return client.outputs[index], nil
	}
	return &awssm.GetSecretValueOutput{}, nil
}

func TestSecretsFailsClosed(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		client  *fakeSecretsClient
		wantErr error
	}{
		{name: "not found", client: &fakeSecretsClient{err: apiFailure{code: "ResourceNotFoundException"}}, wantErr: secret.ErrNotFound},
		{name: "denied", client: &fakeSecretsClient{err: apiFailure{code: "AccessDeniedException"}}, wantErr: secret.ErrDenied},
		{name: "invalid", client: &fakeSecretsClient{err: apiFailure{code: "InvalidParameterException"}}, wantErr: secret.ErrInvalid},
		{name: "unavailable", client: &fakeSecretsClient{err: apiFailure{code: "InternalServiceErrorException"}}, wantErr: secret.ErrUnavailable},
		{name: "oversized", client: &fakeSecretsClient{output: &awssm.GetSecretValueOutput{SecretString: aws.String(strings.Repeat("x", secret.MaxPayloadBytes+1))}}, wantErr: secret.ErrInvalid},
		{name: "empty", client: &fakeSecretsClient{output: &awssm.GetSecretValueOutput{}}, wantErr: secret.ErrInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			resolver, err := NewSecrets(test.client)
			if err != nil {
				t.Fatalf("NewSecrets() error = %v", err)
			}
			reference := mustReference(t, "secret://aws/prod/runner")
			if _, err := resolver.Resolve(context.Background(), reference); !errors.Is(err, test.wantErr) {
				t.Fatalf("Resolve() error = %v, want %v", err, test.wantErr)
			}
		})
	}

	other := mustReference(t, "secret://file/run/secrets/credential")
	resolver, err := NewSecrets(&fakeSecretsClient{})
	if err != nil {
		t.Fatalf("NewSecrets() error = %v", err)
	}
	if _, err := resolver.Resolve(context.Background(), other); !errors.Is(err, secret.ErrInvalid) {
		t.Fatalf("Resolve(file reference) error = %v, want ErrInvalid", err)
	}
}
