package hosted

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	modal "github.com/modal-labs/modal-client/go"
)

const ModalHumanizer = "jialinyyzz/humanizer"
const ModalSmaug = "abacusai/Smaug-Humanizer"

func DeployModalHumanizer(ctx context.Context, credential, app, model string) (string, error) {
	if !slug.MatchString(app) || (model != ModalHumanizer && model != ModalSmaug) {
		return "", errors.New("invalid managed Modal deployment")
	}
	id, secret, ok := modalAPIToken(credential)
	if !ok {
		return "", errors.New("a Modal API token needs an ak- token ID and an as- token secret")
	}
	assets, err := modalAssetsDirectory()
	if err != nil {
		return "", err
	}
	client, err := newModalAPIClient(credential, "")
	if err != nil {
		return "", err
	}
	defer client.Close()
	environment, err := client.Environments.FromContext(ctx)
	if err != nil || environment == nil || !slug.MatchString(environment.Name) {
		return "", errors.New("could not determine the Modal environment; check the API token and workspace access")
	}
	state, err := os.MkdirTemp("", "zentrale-modal-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(state)
	python, err := modalPython(ctx, assets, state)
	if err != nil {
		return "", err
	}
	command := exec.CommandContext(ctx, python, "-m", "modal", "deploy", "--env", environment.Name, "--name", app, "modal_app.py")
	command.Dir = assets
	command.Env = []string{
		"PATH=" + os.Getenv("PATH"), "NO_COLOR=1",
		"MODAL_CONFIG_PATH=" + filepath.Join(state, "modal.toml"),
		"MODAL_TOKEN_ID=" + id, "MODAL_TOKEN_SECRET=" + secret,
		"MODAL_SERVER_URL=https://api.modal.com", "ZENTRALE_HUMANIZER_MODEL=" + model,
		"ZENTRALE_MODAL_NATIVE_ONLY=1", "PYTHONDONTWRITEBYTECODE=1",
	}
	// Keep deployment output private. The CLI can include request metadata in
	// its diagnostics, so only expose selected error categories.
	var diagnostics modalDiagnostic
	command.Stdout, command.Stderr = &diagnostics, &diagnostics
	if err := command.Run(); err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		output := strings.ToLower(diagnostics.tail)
		switch {
		case strings.Contains(output, "no module named modal"), errors.Is(err, exec.ErrNotFound):
			return "", errors.New("Modal deployment runtime is missing from this Zentrale installation")
		case strings.Contains(output, "permission"), strings.Contains(output, "unauthenticated"), strings.Contains(output, "forbidden"):
			return "", errors.New("Modal refused the deployment; check that the API token has deployment access")
		default:
			return "", errors.New("Modal deployment failed; check the build in the Modal dashboard and retry")
		}
	}
	if _, err := client.Functions.FromName(ctx, app, "run_batch", &modal.FunctionFromNameParams{Environment: environment.Name}); err != nil {
		return "", errors.New("the deployed Humanizer function could not be verified in Modal")
	}
	return "modal://" + environment.Name + "/" + app + "/run_batch", nil
}

func modalPython(ctx context.Context, assets, state string) (string, error) {
	if python := os.Getenv("ZENTRALE_MODAL_PYTHON"); python != "" {
		return python, nil // The Zentrale image includes the pinned SDK.
	}
	venv := filepath.Join(state, "venv")
	python := filepath.Join(venv, "bin", "python")
	commands := []*exec.Cmd{
		exec.CommandContext(ctx, "python3", "-m", "venv", venv),
		exec.CommandContext(ctx, python, "-m", "pip", "install", "--quiet", "--disable-pip-version-check", "-r", filepath.Join(assets, "modal-requirements.txt")),
	}
	for _, command := range commands {
		// SDK installation does not need access to the Modal credentials.
		command.Env = []string{"PATH=" + os.Getenv("PATH"), "PIP_NO_CACHE_DIR=1"}
		if err := command.Run(); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			return "", errors.New("could not install the bundled Modal SDK; this Zentrale server needs Python with venv and access to PyPI")
		}
	}
	return python, nil
}

type modalDiagnostic struct{ tail string }

func (log *modalDiagnostic) Write(data []byte) (int, error) {
	const limit = 4096
	log.tail += string(data[max(0, len(data)-limit):])
	log.tail = log.tail[max(0, len(log.tail)-limit):]
	return len(data), nil
}

func modalAssetsDirectory() (string, error) {
	directories := []string{"deploy/humanizer", "../deploy/humanizer"}
	if directory := os.Getenv("ZENTRALE_MODAL_ASSETS"); directory != "" {
		directories = []string{directory}
	}
	for _, directory := range directories {
		if path, err := filepath.Abs(directory); err == nil {
			if _, err := os.Stat(filepath.Join(path, "modal_app.py")); err == nil {
				return path, nil
			}
		}
	}
	return "", errors.New("the Modal deployment template is missing from this Zentrale installation")
}
