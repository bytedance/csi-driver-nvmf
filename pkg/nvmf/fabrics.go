/*
Copyright 2021 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package nvmf

import (
	b64 "encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"strings"

	"github.com/kubernetes-csi/csi-driver-nvmf/pkg/utils"
	"k8s.io/klog"
)

type Connector struct {
	VolumeID      string
	DeviceUUID    string
	TargetNqn     string
	TargetAddr    string
	TargetPort    string
	Transport     string
	HostNqn       string
	RetryCount    int32
	CheckInterval int32
}

func getNvmfConnector(nvmfInfo *nvmfDiskInfo, hostnqn string) *Connector {
	return &Connector{
		VolumeID:   nvmfInfo.VolName,
		DeviceUUID: nvmfInfo.DeviceUUID,
		TargetNqn:  nvmfInfo.Nqn,
		TargetAddr: nvmfInfo.Addr,
		TargetPort: nvmfInfo.Port,
		Transport:  nvmfInfo.Transport,
		HostNqn:    hostnqn,
	}
}

// connector provides a struct to hold all of the needed parameters to make nvmf connection

func _connect(argStr string) error {
	file, err := os.OpenFile("/dev/nvme-fabrics", os.O_RDWR, 0666)
	if err != nil {
		klog.Errorf("Connect: open NVMf fabrics error: %v", err)
		return err
	}

	defer file.Close()

	err = utils.WriteStringToFile(file, argStr)
	if err != nil {
		klog.Errorf("Connect: write arg to connect file error: %v", err)
		return err
	}
	// todo: read file to verify
	lines, err := utils.ReadLinesFromFile(file)
	klog.Infof("Connect: read string %s", lines)
	return nil
}

func _disconnect(sysfs_path string) error {
	file, err := os.OpenFile(sysfs_path, os.O_WRONLY, 0755)
	if err != nil {
		return err
	}
	err = utils.WriteStringToFile(file, "1")
	if err != nil {
		klog.Errorf("Disconnect: write 1 to delete_controller error: %v", err)
		return err
	}
	return nil
}

func disconnectSubsysWithHostNqn(nqn, hostnqn, ctrl string) (res bool) {
	sysfs_nqn_path := fmt.Sprintf("%s/%s/subsysnqn", SYS_NVMF, ctrl)
	sysfs_hostnqn_path := fmt.Sprintf("%s/%s/hostnqn", SYS_NVMF, ctrl)
	sysfs_del_path := fmt.Sprintf("%s/%s/delete_controller", SYS_NVMF, ctrl)

	file, err := os.Open(sysfs_nqn_path)
	if err != nil {
		klog.Errorf("Disconnect: open file %s err: %v", file.Name(), err)
		return false
	}
	defer file.Close()

	lines, err := utils.ReadLinesFromFile(file)
	if err != nil {
		klog.Errorf("Disconnect: read file %s err: %v", file.Name(), err)
		return false
	}

	if lines[0] != nqn {
		klog.Warningf("Disconnect: not this subsystem, skip")
		return false
	}

	file, err = os.Open(sysfs_hostnqn_path)
	if err != nil {
		klog.Errorf("Disconnect: open file %s err: %v", sysfs_hostnqn_path, err)
		return false
	}
	defer file.Close()

	lines, err = utils.ReadLinesFromFile(file)
	if err != nil {
		klog.Errorf("Disconnect: read file %s err: %v", file.Name(), err)
		return false
	}

	if lines[0] != hostnqn {
		klog.Warningf("Disconnect: not this subsystem, skip")
		return false
	}

	err = _disconnect(sysfs_del_path)
	if err != nil {
		klog.Errorf("Disconnect: disconnect error: %s", err)
		return false
	}

	return true
}

func disconnectSubsys(nqn, ctrl string) (res bool) {
	sysfs_nqn_path := fmt.Sprintf("%s/%s/subsysnqn", SYS_NVMF, ctrl)
	sysfs_del_path := fmt.Sprintf("%s/%s/delete_controller", SYS_NVMF, ctrl)

	file, err := os.Open(sysfs_nqn_path)
	if err != nil {
		klog.Errorf("Disconnect: open file %s err: %v", file.Name(), err)
		return false
	}
	defer file.Close()

	lines, err := utils.ReadLinesFromFile(file)
	if err != nil {
		klog.Errorf("Disconnect: read file %s err: %v", file.Name(), err)
		return false
	}

	if lines[0] != nqn {
		klog.Warningf("Disconnect: not this subsystem, skip")
		return false
	}

	err = _disconnect(sysfs_del_path)
	if err != nil {
		klog.Errorf("Disconnect: disconnect error: %s", err)
		return false
	}

	return true
}

func disconnectByNqn(nqn, hostnqn string) int {
	ret := 0
	if len(nqn) > NVMF_NQN_SIZE {
		klog.Errorf("Disconnect: nqn %s is too long ", nqn)
		return -EINVAL
	}

	// delete hostnqn file
	hostnqnPath := strings.Join([]string{RUN_NVMF, nqn, b64.StdEncoding.EncodeToString([]byte(hostnqn))}, "/")
	os.Remove(hostnqnPath)

	devices, err := ioutil.ReadDir(SYS_NVMF)
	if err != nil {
		klog.Errorf("Disconnect: readdir %s err: %s", SYS_NVMF, err)
		return -ENOENT
	}

	for _, device := range devices {
		if disconnectSubsysWithHostNqn(nqn, hostnqn, device.Name()) {
			ret++
		}
	}

	nqnPath := strings.Join([]string{RUN_NVMF, nqn}, "/")
	hostnqns, err := ioutil.ReadDir(nqnPath)
	if err != nil {
		klog.Errorf("Disconnect: readdir %s err: %v", nqnPath, err)
		return -ENOENT
	}
	if len(hostnqns) <= 0 {
		if ret == 0 {
			klog.Infof("Fallback because you have no hostnqn supports!")

			devices, err := ioutil.ReadDir(SYS_NVMF)
			if err != nil {
				klog.Errorf("Disconnect: readdir %s err: %s", SYS_NVMF, err)
				return -ENOENT
			}

			for _, device := range devices {
				if disconnectSubsys(nqn, device.Name()) {
					ret++
				}
			}
		}

		os.RemoveAll(nqnPath)
	}

	return ret
}

// connect to volume to this node and return devicePath
func (c *Connector) Connect() (string, error) {
	if c.RetryCount == 0 {
		c.RetryCount = 10
	}
	if c.CheckInterval == 0 {
		c.CheckInterval = 1
	}

	if c.RetryCount < 0 || c.CheckInterval < 0 {
		return "", fmt.Errorf("Invalid RetryCount and CheckInterval combinaitons "+
			"RetryCount: %d, CheckInterval: %d ", c.RetryCount, c.CheckInterval)
	}

	if strings.ToLower(c.Transport) != "tcp" && strings.ToLower(c.Transport) != "rdma" {
		return "", fmt.Errorf("csi transport only support tcp/rdma ")
	}

	baseString := fmt.Sprintf("nqn=%s,transport=%s,traddr=%s,trsvcid=%s,hostnqn=%s", c.TargetNqn, c.Transport, c.TargetAddr, c.TargetPort, c.HostNqn)
	devicePath := strings.Join([]string{"/dev/disk/by-id/nvme-uuid", c.DeviceUUID}, ".")

	// connect to nvmf disk
	err := _connect(baseString)
	if err != nil {
		return "", err
	}
	klog.Infof("Connect Volume %s success nqn: %s, hostnqn: %s", c.VolumeID, c.TargetNqn, c.HostNqn)
	retries := int(c.RetryCount / c.CheckInterval)
	if exists, err := waitForPathToExist(devicePath, retries, int(c.CheckInterval), c.Transport); !exists {
		klog.Errorf("connect nqn %s error %v, rollback!!!", c.TargetNqn, err)
		ret := disconnectByNqn(c.TargetNqn, c.HostNqn)
		if ret < 0 {
			klog.Errorf("rollback error !!!")
		}
		return "", err
	}

	// create nqn directory
	nqnPath := strings.Join([]string{RUN_NVMF, c.TargetNqn}, "/")
	if err := os.MkdirAll(nqnPath, 0750); err != nil {
		klog.Errorf("create nqn directory %s error %v, rollback!!!", c.TargetNqn, err)
		ret := disconnectByNqn(c.TargetNqn, c.HostNqn)
		if ret < 0 {
			klog.Errorf("rollback error !!!")
		}
		return "", err
	}

	// create hostnqn file
	hostnqnPath := strings.Join([]string{RUN_NVMF, c.TargetNqn, b64.StdEncoding.EncodeToString([]byte(c.HostNqn))}, "/")
	file, err := os.Create(hostnqnPath)
	if err != nil {
		klog.Errorf("create hostnqn file %s:%s error %v, rollback!!!", c.TargetNqn, c.HostNqn, err)
		ret := disconnectByNqn(c.TargetNqn, c.HostNqn)
		if ret < 0 {
			klog.Errorf("rollback error !!!")
		}
		return "", err
	}
	defer file.Close()

	klog.Infof("After connect we're returning devicePath: %s", devicePath)
	return devicePath, nil
}

// we disconnect only by nqn
func (c *Connector) Disconnect() error {
	ret := disconnectByNqn(c.TargetNqn, c.HostNqn)
	if ret < 0 {
		return fmt.Errorf("Disconnect: failed to disconnect by nqn: %s ", c.TargetNqn)
	}

	return nil
}

// PersistConnector persists the provided Connector to the specified file (ie /var/lib/pfile/myConnector.json)
func persistConnectorFile(c *Connector, filePath string) error {
	f, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("error creating nvmf persistence file %s: %s", filePath, err)
	}
	defer f.Close()
	encoder := json.NewEncoder(f)
	if err = encoder.Encode(c); err != nil {
		return fmt.Errorf("error encoding connector: %v", err)
	}
	return nil

}

func removeConnectorFile(targetPath string) {
	// todo: here maybe be attack for os.Remove can operate any file, fix?
	if err := os.Remove(targetPath + ".json"); err != nil {
		klog.Errorf("DetachDisk: Can't remove connector file: %s", targetPath)
	}
	if err := os.RemoveAll(targetPath); err != nil {
		klog.Errorf("DetachDisk: failed to remove mount path Error: %v", err)
	}
}

func GetConnectorFromFile(filePath string) (*Connector, error) {
	f, err := ioutil.ReadFile(filePath)
	if err != nil {
		return &Connector{}, err

	}
	data := Connector{}
	err = json.Unmarshal([]byte(f), &data)
	if err != nil {
		return &Connector{}, err
	}

	return &data, nil
}
