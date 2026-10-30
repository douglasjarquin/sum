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
	Schema   int         `json:"schema"`
	Task     string      `json:"task"`
	Delivery string      `json:"delivery"`
	Digest   string      `json:"digest"`
	Receipt  WakeReceipt `json:"receipt"`
}

func consumptionPath(s *store.Store, task string, receipt WakeReceipt) (string, error) {
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
func syncConsumption(path string) error {
	for _, target := range []string{path, filepath.Dir(path), filepath.Dir(filepath.Dir(path))} {
		file, err := os.Open(target)
		if err != nil {
			return err
		}
		err = file.Sync()
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
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
func archiveConsumption(s *store.Store, task, row *ordjson.Object, receipt WakeReceipt) error {
	taskID, _ := routeValue(task, "id").(string)
	if _, err := consumptionDirectory(s, taskID); err != nil {
		return err
	}
	if err := validateProofCoverage(s, task, row, []deliveryProof{{Consumption: &receipt}}, false); err != nil {
		return err
	}
	value := consumedDelivery{Schema: 1, Task: taskID, Delivery: receipt.Delivery, Digest: digest(row), Receipt: receipt}
	path, err := consumptionPath(s, taskID, receipt)
	if err != nil {
		return err
	}
	prior, err := readConsumption(path)
	if err == nil {
		if digest(prior) != digest(value) {
			return fmt.Errorf("consumed delivery evidence conflicts with its original receipt")
		}
		return syncConsumption(path)
	}
	if !os.IsNotExist(err) {
		return err
	}
	if err = s.CheckWritable(); err != nil {
		return err
	}
	object, err := ordjson.FromValue(value)
	if err != nil {
		return err
	}
	if err = ordjson.WriteFile(path, object); err != nil {
		return err
	}
	return syncConsumption(path)
}
func archiveWakeConsumptions(s *store.Store, w *Wake) error {
	synced := false
	for _, receipt := range w.Receipts {
		if receipt.Result != "consumed" || receipt.Boundary == nil || len(receipt.Accepted) == 0 {
			continue
		}
		if !synced {
			if err := syncConsumption(s.WakePath(w.Endpoint())); err != nil {
				return fmt.Errorf("sync original consumed wake: %w", err)
			}
			synced = true
		}
		seen := map[string]bool{}
		for _, included := range receipt.Accepted {
			if seen[included.Task] {
				continue
			}
			seen[included.Task] = true
			task, err := s.ReadTask(included.Task)
			if err != nil {
				return err
			}
			history, err := ReadReturns(s, included.Task)
			if err != nil {
				return err
			}
			rows, ok := routeValue(history, "deliveries").([]any)
			if !ok {
				return fmt.Errorf("invalid delivery history while retaining consumption")
			}
			var row *ordjson.Object
			for _, raw := range rows {
				candidate, _ := raw.(*ordjson.Object)
				if candidate == nil {
					return fmt.Errorf("invalid delivery row while retaining consumption")
				}
				if routeValue(candidate, "id") == receipt.Delivery {
					if row != nil {
						return fmt.Errorf("duplicate delivery while retaining consumption")
					}
					row = candidate
				}
			}
			if row == nil {
				continue
			}
			switch routeValue(row, "state") {
			case "in-flight", "uncertain", "unknown":
			default:
				continue
			}
			if _, exists := row.Get("coverage"); !exists {
				continue
			}
			coverage, err := coverageOf(row)
			if err != nil {
				return err
			}
			relevant := false
			for _, c := range coverage {
				for _, a := range receipt.Accepted {
					if a.Task == included.Task && a.ID == c.ID && a.InputRevision == c.Revision {
						relevant = true
					}
				}
			}
			if !relevant {
				continue
			}
			if err = archiveConsumption(s, task, row, receipt); err != nil {
				return fmt.Errorf("retain consumed delivery %s: %w", receipt.Delivery, err)
			}
		}
	}
	return nil
}
func archivePriorWakeConsumptions(s *store.Store, w *Wake) error {
	prior, status := ReadWake(s, w.Endpoint())
	if status.State == WakeAbsent {
		return nil
	}
	if status.State != WakeOK {
		return fmt.Errorf("prior wake cannot be retained: %s", status.Diagnostic)
	}
	return archiveWakeConsumptions(s, prior)
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
