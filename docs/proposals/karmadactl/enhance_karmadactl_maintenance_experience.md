---
title: Service discovery with native Kubernetes naming and resolution
authors:
- "@hulizhe"
- "@zhzhuang-zju"
reviewers:
- "@RainbowMango"

approvers:
- "@RainbowMango"

creation-date: 2024-07-22
---

# Enhance karmadactl operation and maintenance experience

## Summary

当前karmadactl实现了get、describe等命令在多集群场景下的部分应用，但缺少对karmada资源的增删改查。此外，部分kubectl的命令未继承到多集群场景。

因此，本提案计划补齐 karmadactl 在多集群场景下的功能，进一步提高karmadactl的运维体验。主要包括：
- 新增karmadactl get/describe 对karmada资源的增删改查的能力
- 完成karmadactl create/label/edit等命令的功能实现，

## Motivation

<!--
This section is for explicitly listing the motivation, goals, and non-goals of
this KEP.  Describe why the change is important and the benefits to users.
-->

当前，由于karmadactl在多集群场景下的能力不全，实际运维中，往往将karmadactl和kubectl配合使用。这会面临不同使用场景下karmadactl和kubectl命令的切换、kubectl需要频繁切换上下文和资源浪费等问题：
- 假设Karmada有一个deployment `foo` 并且被分发到了成员集群member1和member2
- 如果希望查看karmada和各个成员集群下的 `foo`
- 首先，需要用kubectl get --kubeconfig $HOME/.kube/karmada.config --context karmada-apiserver命令查看karmada的deployment `foo`
- 接下来切换为karmadactl get命令查看成员集群下的deployment `foo`
如果karmadactl可以查看karmada的资源，那么，只需要`karmadactl get`便可完成以上需求。

### Goals

- 提供karmadactl增删改查karmada资源的能力
- 补齐kubectl仍未被karmadactl继承的能力，包括但不限于 karmadactl create, karmadactl label 和 karmadactl edit.

### Non-Goals

- 命令输出显示效果的优化
- 去除代码仓kubectl的使用

## Proposal

提供karmadactl增删改查karmada资源的能力后，karmadactl命令依据具体功能可将使用范围分为：
- karmada
- 某一个成员集群
- 一个或多个成员集群
- karmada或某一个成员集群
- karmada和一个或多个成员集群

为统一不同命令不同使用范围的表现，约定：
- 如果命令的作用范围是控制面和成员集群，那么默认的作用范围为控制面，需要配置参数来扩大命令的作用范围到成员集群。
- 如果命令的作用范围只是成员集群，那么无默认作用范围，需要通过参数来限定具体的成员集群或者是成员集群列表。
- 如果命令的作用范围只能是某一个集群(可以是控制面，也可以是成员集群)， 那么默认的作用范围为控制面，需要配置参数来修改作用集群。

当前 karmadactl 的参数只能满足 karmadactl 查看或修改成员集群资源的能力，所以首先需要对karmadactl的参数进行设计和修改，使其能满足以上多种场景。

其次，karmadactl命令依据是否继承于kubectl可分为：
- 继承至kubectl，是从单集群扩展到多集群的能力
- 多集群特有能力

为了提供kubectl切换到karmadactl的无缝体验，kubectl和karmadactl相同命令的使用体验尽量保持一致。

由于直接修改成员集群的资源，可能会导致karmada和成员集群的冲突，因此，karmadactl不应提供直接修改成员集群资源的能力


### User Stories (Optional)

<!--
Detail the things that people will be able to do if this KEP is implemented.
Include as much detail as possible so that people can understand the "how" of
the system. The goal here is to make this feel real for users without getting
bogged down.
-->

#### Story 1

作为一名运维人员，我希望能查看当前 karmada 以及成员集群的资源 without 上下文切换和CLI切换。

**Scenario**:

1. Given that karmada和成员集群均有deployment `foo`.
2. 当我使用 `karmadactl get`with一些参数时，可以查看到karmada以及所有成员集群的deployment `foo`.

### Notes/Constraints/Caveats (Optional)

<!--
What are the caveats to the proposal?
What are some important details that didn't come across above?
Go in to as much detail as necessary here.
This might be a good place to talk about core concepts and how they relate.
-->

### Risks and Mitigations

<!--
What are the risks of this proposal, and how do we mitigate?

How will security be reviewed, and by whom?

How will UX be reviewed, and by whom?

Consider including folks who also work outside the SIG or subproject.
-->

## Design Details

<!--
This section should contain enough information that the specifics of your
change are understandable. This may include API specs (though not always
required) or even code snippets. If there's any ambiguity about HOW your
proposal will be implemented, this is the place to discuss them.
-->

### CLI flags changes

This proposal proposes new flags `ignore-host` and `all-clusters` in karmadactl flag set.

| name           | shorthand      | default | usage                                                   |
|----------------|----------------|---------|---------------------------------------------------------|
| ignore-karmada | ignore-karmada | false   | Used to control whether karmada's resources are ignored |
| all-clusters   | all-clusters   | false   | Used to specify whether to include all member clusters  |

With these flag, we will:
* 当karmadactl 作用范围是控制面和成员集群时，我们可以指定忽略karmada的资源，只操作成员集群的资源。
* 当karmadactl 作用范围包含成员集群时，我们可以指定是否操作全部成员集群的资源。

#### flag set under different usage scopes
- usage scope: karmada

flag set: None.

相关命令：edit、create、delete、label 和 annotate 

- usage scope: 某一个成员集群

flag set:

| name    | shorthand | default | usage                                 |
|---------|-----------|---------|---------------------------------------|
| cluster | C         | none    | Used to specify target member cluster |

相关命令：logs, exec 

具体表现：
<ol>
<li>当没有设置cluster时，提示“请至少指定一个目标集群”</li>
</ol>

- usage scope: 一个或多个成员集群

flag set:

| name         | shorthand    | default | usage                                                  |
|--------------|--------------|---------|--------------------------------------------------------|
| clusters     | C            | none    | Used to specify target member clusters                 |
| all-clusters | all-clusters | false   | Used to specify whether to include all member clusters |

相关命令：top-pod， top-node

具体表现：
<ol>
<li>当没有设置clusters且all-clusters值为false时，提示“请至少指定一个目标集群”</li>
<li>当设置了clusters且all-clusters值为true时，作用范围是所有成员集群</li>
</ol>

- usage scope: karmada或某一个成员集群

flag set:

| name    | shorthand | default | usage                                 |
|---------|-----------|---------|---------------------------------------|
| cluster | C         | none    | Used to specify target member cluster |

相关命令：describe

具体表现：
<ol>
<li>当没有设置cluster时，作用范围是karmada”</li>
<li>当设置了cluster时，作用范围是成员集群cluster</li>
</ol>

- usage scope: karmada和一个或多个成员集群

flag set:

| name           | shorthand      | default | usage                                                   |
|----------------|----------------|---------|---------------------------------------------------------|
| clusters       | C              | none    | Used to specify target member clusters                  |
| all-clusters   | all-clusters   | false   | Used to specify whether to include all member clusters  |
| ignore-karmada | ignore-karmada | false   | Used to control whether karmada's resources are ignored |

相关命令：get

具体表现：
<ol>
<li>当没有设置clusters, all-clusters值为false且ignore-karmada值为true时，提示“请至少指定一个目标集群”</li>
<li>当设置了clusters且all-clusters值为true时，作用范围是karmada和所有成员集群</li>
<li>当设置了clusters时，作用范围是karmada和clusters指定的成员集群列表</li>
</ol>

### 新增命令

为了补齐 karmadactl 增删改查资源的基本能力，本提案计划补齐：
- karmadactl edit、create、delete、label 和 annotate 命令，实现 karmadactl 对控制面资源 CURD 的能力。具体能力和 kubectl 保持一致
- karmadactl top node，实现 karmadactl 对成员集群节点资源使用量的查看。

## Alternatives

<!--
What other approaches did you consider, and why did you rule them out? These do
not need to be as detailed as the proposal, but should include enough
information to express the idea and why it was not acceptable.
-->

<!--
Note: This is a simplified version of kubernetes enhancement proposal template.
https://github.com/kubernetes/enhancements/tree/3317d4cb548c396a430d1c1ac6625226018adf6a/keps/NNNN-kep-template
-->
