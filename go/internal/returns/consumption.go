package returns

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/douglasjarquin/sum/go/internal/ordjson"
	"github.com/douglasjarquin/sum/go/internal/store"
)

const ConsumptionDirectory = "delivery-consumptions"

type consumedDelivery struct {
	Schema   int               `json:"schema"`
	Task     string            `json:"task"`
	Delivery string            `json:"delivery"`
	Digest   string            `json:"digest"`
	Receipt  historicalReceipt `json:"receipt"`
}

func consumptionPath(s *store.Store, task string, receipt historicalReceipt) (string, error) {
	dir, err := s.TaskPath(task)
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, ConsumptionDirectory, digest(receipt.Delivery)+"-"+digest(receipt)+".json"), nil
}
func readConsumption(path string) (*consumedDelivery, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("consumed delivery evidence must be a regular file")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var value consumedDelivery
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&value); err != nil {
		return nil, err
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("consumed delivery evidence has trailing data")
	}
	if value.Schema != 1 || value.Task == "" || value.Delivery == "" || value.Digest == "" || value.Receipt.Delivery != value.Delivery {
		return nil, fmt.Errorf("invalid consumed delivery evidence")
	}
	return &value, nil
}
func consumptionDirectory(s *store.Store, task string) (string, error) {
	dir, err := s.TaskPath(task)
	if err != nil {
		return "", err
	}
	dir = filepath.Join(dir, ConsumptionDirectory)
	if info, err := os.Lstat(dir); err == nil {
		if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("consumed delivery evidence directory must not be redirected")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return dir, nil
}
func retainedConsumptions(s *store.Store, task string, row *ordjson.Object) ([]deliveryProof, error) {
	dir, err := consumptionDirectory(s, task)
	if err != nil {
		return nil, err
	}
	id, _ := routeValue(row, "id").(string)
	paths, err := filepath.Glob(filepath.Join(dir, digest(id)+"-*.json"))
	if err != nil {
		return nil, err
	}
	proofs := []deliveryProof{}
	for _, path := range paths {
		value, err := readConsumption(path)
		if err != nil {
			return nil, err
		}
		expected, err := consumptionPath(s, task, value.Receipt)
		if err != nil {
			return nil, err
		}
		if value.Task != task || value.Delivery != id || value.Digest != digest(row) || expected != path {
			return nil, fmt.Errorf("retained consumed delivery evidence is stale or unrelated")
		}
		receipt := value.Receipt
		proofs = append(proofs, deliveryProof{Consumption: &receipt})
	}
	return proofs, nil
}
