package local

import (
	"fmt"
	"os"
	"path/filepath"
)

func FindWorkspaceRoot() (string, error) {
	workingDirectory, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("could not get working directory: %v", err)
	}
	return findWorkspaceRoot(workingDirectory)
}

func findWorkspaceRoot(root string) (string, error) {
	if _, err := os.Stat(filepath.Join(root, "WORKSPACE")); err == nil {
		return root, nil
	}

	if _, err := os.Stat(filepath.Join(root, "WORKSPACE.bazel")); err == nil {
		return root, nil
	}

	parentDirectory := filepath.Dir(root)
	if parentDirectory == root {
		return "", nil
	}

	return findWorkspaceRoot(parentDirectory)
}
