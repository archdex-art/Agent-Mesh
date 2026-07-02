import pytest

from agentmesh.chaos import (
    ChaosInjectedError,
    ChaosPolicy,
    ErrorFault,
    LatencyFault,
    apply_fault,
    configure_chaos,
    get_chaos_policy,
)


def test_policy_disabled_by_default_returns_no_faults():
    policy = ChaosPolicy(enabled=False, faults_by_tool={"search": [ErrorFault()]})
    assert policy.faults_for("search") == []
    assert policy.maybe_apply("search") is None


def test_policy_enabled_returns_configured_faults():
    fault = ErrorFault()
    policy = ChaosPolicy(enabled=True, faults_by_tool={"search": [fault]})
    assert policy.faults_for("search") == [fault]


def test_policy_returns_empty_for_unconfigured_tool():
    policy = ChaosPolicy(enabled=True, faults_by_tool={"search": [ErrorFault()]})
    assert policy.faults_for("call_model") == []


def test_maybe_apply_deterministic_probability_one():
    fault = ErrorFault(probability=1.0)
    policy = ChaosPolicy(enabled=True, faults_by_tool={"search": [fault]})
    assert policy.maybe_apply("search") is fault


def test_maybe_apply_probability_zero_never_fires():
    fault = ErrorFault(probability=0.0)
    policy = ChaosPolicy(enabled=True, faults_by_tool={"search": [fault]})
    for _ in range(50):
        assert policy.maybe_apply("search") is None


def test_maybe_apply_is_reproducible_with_seed():
    fault = ErrorFault(probability=0.5)
    a = configure_chaos(enabled=True, faults_by_tool={"search": [fault]}, seed=42)
    results_a = [a.maybe_apply("search") is not None for _ in range(20)]

    b = configure_chaos(enabled=True, faults_by_tool={"search": [fault]}, seed=42)
    results_b = [b.maybe_apply("search") is not None for _ in range(20)]

    assert results_a == results_b


def test_maybe_apply_only_fires_one_fault_per_call():
    error_fault = ErrorFault(probability=1.0)
    latency_fault = LatencyFault(seconds=0.01, probability=1.0)
    policy = ChaosPolicy(enabled=True, faults_by_tool={"search": [error_fault, latency_fault]})
    result = policy.maybe_apply("search")
    assert result is error_fault  # first configured fault wins


def test_apply_fault_latency_sleeps(monkeypatch):
    slept = []
    monkeypatch.setattr("agentmesh.chaos.time.sleep", lambda s: slept.append(s))
    apply_fault(LatencyFault(seconds=2.5))
    assert slept == [2.5]


def test_apply_fault_error_raises_chaos_injected_error():
    fault = ErrorFault(message="boom")
    with pytest.raises(ChaosInjectedError, match="boom"):
        apply_fault(fault)


def test_apply_fault_error_raises_custom_exception_type():
    fault = ErrorFault.timeout()
    with pytest.raises(TimeoutError):
        apply_fault(fault)


def test_error_fault_http_error_convenience_constructor():
    fault = ErrorFault.http_error(status_code=503)
    with pytest.raises(ChaosInjectedError, match="503"):
        apply_fault(fault)


def test_configure_chaos_updates_module_default():
    configure_chaos(enabled=True, faults_by_tool={"x": [ErrorFault()]}, seed=1)
    policy = get_chaos_policy()
    assert policy.enabled is True
    assert "x" in policy.faults_by_tool


def test_configure_chaos_defaults_to_enabled_true():
    policy = configure_chaos()
    assert policy.enabled is True


def test_chaos_injected_error_carries_fault_type():
    try:
        apply_fault(ErrorFault(message="m"))
    except ChaosInjectedError as e:
        assert e.fault_type == "error"
    else:
        pytest.fail("expected ChaosInjectedError")
