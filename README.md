# OpenShift Capacity Advisor

Operator Go (Operator SDK) que mede capacidade do cluster OpenShift e recomenda
capacidade adicional com base em **targets de utilização** (como no mockup Overview).

Código propositalmente simples: o reconcile só faz `collect → advise → status`.

## Como o reconcile funciona (5 passos)

1. Lê o CR `CapacityAdvisor` (targets: CPU/Memória/Pods %).
2. **Collector** lista Nodes, Pods e MachineConfigPools e soma allocatable vs requests.
3. **Advisor** aplica a fórmula (sem falar com a API):
   ```
   usage = requested / allocatable
   se usage > target:
     additional = (requested / target) - allocatable
   ```
4. Grava o resultado em `status` (cluster, pools, recommendations).
5. Requeue em 30s (“Last updated”).

## Layout do código

| Caminho | Responsabilidade |
|---------|------------------|
| `api/v1alpha1/capacityadvisor_types.go` | Spec (targets) + Status (números do dashboard) |
| `internal/collector/` | Só API Kubernetes/OpenShift |
| `internal/advisor/` | Só matemática (+ testes com números do mockup) |
| `internal/controller/` | Orquestra os 5 passos acima |

## Pré-requisitos

- Go 1.22+
- `kubectl` / `oc` apontando para um cluster (OpenShift preferível)
- Opcional: `operator-sdk` se for re-scaffolding

## Build e testes

```bash
make generate manifests
go test ./internal/advisor/
make build
```

## Instalação em produção

Pré-requisitos: `oc`/`kubectl` logado no cluster e um registry que o cluster consiga puxar (Quay, registry interno, etc.).

```bash
# 1) Imagem (troque pelo seu registry)
export IMG=quay.io/<seu-user>/ocp-capacity-advisor:v0.1.0

# 2) Build + push
make docker-build docker-push IMG=$IMG

# 3) Deploy no cluster (namespace openshift-advisor)
#    Instala CRD, RBAC, ServiceAccount e Deployment do manager
make deploy IMG=$IMG

# 4) Confirme que o manager subiu
kubectl -n openshift-advisor get pods

# 5) Crie o CR com os targets de utilização
kubectl apply -f config/samples/advisor_v1alpha1_capacityadvisor.yaml

# 6) Veja o status (capacidade + recomendações)
kubectl get capacityadvisor cluster -o yaml
```

Campos úteis no status:

- `status.cluster` — CPU, memória, pods e nodes
- `status.pools` — breakdown por MachineConfigPool
- `status.recommendations` — capacidade adicional e nós estimados

### Remover do cluster

```bash
kubectl delete -f config/samples/advisor_v1alpha1_capacityadvisor.yaml
make undeploy
make uninstall
```

### Manifesto único (opcional)

Gera um YAML consolidado para aplicar com `kubectl apply -f`:

```bash
export IMG=quay.io/<seu-user>/ocp-capacity-advisor:v0.1.0
make build-installer IMG=$IMG
kubectl apply -f dist/install.yaml
```

## Desenvolvimento local (sem imagem)

Só o CRD no cluster; o manager roda na sua máquina:

```bash
make install
make run
kubectl apply -f config/samples/advisor_v1alpha1_capacityadvisor.yaml
```
## Targets (Spec)

| Campo | Default | Significado |
|-------|---------|-------------|
| `cpuTargetPercent` | 70 | Manter requests de CPU ≤ 70% do allocatable |
| `memoryTargetPercent` | 70 | Idem para memória |
| `podsTargetPercent` | 80 | Densidade de pods ≤ 80% da capacidade |

## Notas

- Usa **requests** (reserva de scheduler), não uso real (Metrics/Prometheus).
- Sem MachineConfigPool (Kubernetes puro), agrupa por labels `node-role.kubernetes.io/*`.
- Pool `master` / control-plane: recomenda “Consider upgrade” em vez de scale-out.
