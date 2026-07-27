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

## Deploy rápido

```bash
# Instala CRD + RBAC + Deployment do manager
make deploy IMG=<sua-imagem>

# Ou só o CRD, e rode o manager localmente contra o cluster:
make install
make run
```

Crie o CR de exemplo:

```bash
kubectl apply -f config/samples/advisor_v1alpha1_capacityadvisor.yaml
kubectl get capacityadvisor cluster -o yaml
```

Olhe especialmente:

- `status.cluster` — cards do topo (CPU, memória, pods, nodes)
- `status.pools` — tabela por MachineConfigPool
- `status.recommendations` — +cores / +memória / +pods / nós estimados

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
