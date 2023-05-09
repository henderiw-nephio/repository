# repository

Repository is a repo that contains a controller that handles the lifecycle of repositories in `gitea`.
It acts on a CRS repository.infra.nephio.org

Based on the Repository CR the following child resources are managed:
- repo in gitea
- access token to access the repo in gitea
- secret in k8s cluster with the access token retrieved from gitea

## build

```
dcoker build; docker push
```

## install

```
kpt pkg apply blueprint/repository
```

## exmaple

```
apiVersion: infra.nephio.org/v1alpha1
kind: Repository
metadata:
  name: edge
spec:
```