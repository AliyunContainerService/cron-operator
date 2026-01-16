# cron-operator

[![Go Report Card](https://goreportcard.com/badge/github.com/AliyunContainerService/cron-operator)](https://goreportcard.com/report/github.com/AliyunContainerService/cron-operator)
[![Integration Test](https://github.com/AliyunContainerService/cron-operator/actions/workflows/integration.yaml/badge.svg)](https://github.com/AliyunContainerService/cron-operator/actions/workflows/integration.yaml)
[![GitHub release](https://img.shields.io/github/v/release/AliyunContainerService/cron-operator)](https://github.com/AliyunContainerService/cron-operator/releases)
[![License](https://img.shields.io/badge/License-Apache%202.0-blue.svg)](https://opensource.org/licenses/Apache-2.0)

A Kubernetes operator that enables cron-based scheduling for machine learning training workloads using standard cron expressions.

## Description

The cron-operator extends Kubernetes with custom scheduling capabilities for ML training workloads. Built on the Operator pattern and Custom Resource Definitions (CRDs), it allows users to schedule Kubeflow training jobs (PyTorchJob, TFJob) using familiar cron syntax. The operator handles job lifecycle management, including concurrency control, history retention, and status tracking, providing a robust solution for automated ML training pipelines.

### Key Features

- **Cron-based Scheduling**: Schedule ML training workloads using standard cron expressions (e.g., `*/5 * * * *` for every 5 minutes)
- **Multiple Workload Support**: Compatible with Kubeflow PyTorchJob and TFJob resources
- **Flexible Concurrency Policies**:
  - `Allow`: Run jobs concurrently without restrictions
  - `Forbid`: Skip new executions if previous job is still running
  - `Replace`: Cancel running job and start new execution
- **History Management**: Configurable retention of finished job records with automatic cleanup
- **Execution Control**: Suspend scheduling or set deadline timestamps for time-bound operations
- **Status Tracking**: Monitor active jobs and view historical execution records
- **Kubernetes-native**: Fully integrated with Kubernetes RBAC, events, and API conventions

### Architecture

The cron-operator follows the Kubernetes Operator pattern:

1. **Custom Resource Definition (CRD)**: The `Cron` CRD defines the desired scheduling configuration, including cron schedule, workload template, and policies
2. **Controller**: A reconciliation loop monitors Cron resources and manages workload lifecycle:
   - Calculates next execution time based on cron schedule
   - Creates workload instances from templates at scheduled times
   - Enforces concurrency policies and manages active workloads
   - Updates status with execution history and active job references
3. **Workload Templates**: Generic template mechanism supports any Kubernetes workload type through `runtime.RawExtension`
4. **Status Management**: Maintains lists of active jobs and historical records with timestamps and final states

## Getting Started

### Prerequisites

- go version v1.25.5+
- docker version 17.03+.
- kubectl version v1.11.3+.
- Access to a Kubernetes v1.11.3+ cluster.

### To Deploy on the cluster

**Build and push your image to the location specified by `IMG`:**

```sh
make docker-build docker-push IMG=<some-registry>/cron-operator:tag
```

**NOTE:** This image ought to be published in the personal registry you specified.
And it is required to have access to pull the image from the working environment.
Make sure you have the proper permission to the registry if the above commands don’t work.

**Install the CRDs into the cluster:**

```sh
make install
```

**Deploy the Manager to the cluster with the image specified by `IMG`:**

```sh
make deploy IMG=<some-registry>/cron-operator:tag
```

> **NOTE**: If you encounter RBAC errors, you may need to grant yourself cluster-admin
privileges or be logged in as admin.

**Create instances of your solution**
You can apply the samples (examples) from the config/sample:

```sh
kubectl apply -k config/samples/
```

>**NOTE**: Ensure that the samples has default values to test it out.

### To Uninstall

**Delete the instances (CRs) from the cluster:**

```sh
kubectl delete -k config/samples/
```

**Delete the APIs(CRDs) from the cluster:**

```sh
make uninstall
```

**UnDeploy the controller from the cluster:**

```sh
make undeploy
```

## Contributor Guide

For contributing to Cron Operator, please refer to [Contributor Guide](CONTRIBUTING.md).
