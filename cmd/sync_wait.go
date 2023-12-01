/*
 Copyright 2022 Juicedata Inc

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

package main

import (
	"github.com/juicedata/juicefs-csi-driver/pkg/config"
	"github.com/juicedata/juicefs-csi-driver/pkg/sync"
	"github.com/spf13/cobra"
	corev1 "k8s.io/api/core/v1"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	"os"
	ctrl "sigs.k8s.io/controller-runtime"
)

func init() {
	utilruntime.Must(corev1.AddToScheme(scheme))
}
func parseSyncWaitConfig() {
	config.DstPV = dstPV
	config.SourcePath = sourcePath
	config.Mixture = mixture
	config.Namespace = os.Getenv("CONTROLLER_NAMESPACE")
	if url := os.Getenv("CONTROLLER_URL"); url != "" {
		config.ControllerURL = url
	} else {
		config.ControllerURL = "http://sync-controller.kube-system.svc.cluster.local"
	}

}
func syncWaitCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "syncWait",
		Short: "Waiting for Synchronization to Complete or Checking Synchronization Status",
		Run: func(cmd *cobra.Command, args []string) {
			syncWaitRun()
		},
	}
	cmd.Flags().StringArrayVar(&sourcePath, "source-path", []string{}, "Source of Files to Synchronize")
	cmd.Flags().StringVar(&dstPV, "dst-pv", "", "Target PV")
	return cmd
}
func syncWaitRun() {
	parseSyncWaitConfig()
	ctx := ctrl.SetupSignalHandler()
	checkStatus := sync.CheckSyncStatus{PV: config.DstPV}
	checkStatus.Run(ctx)
}
