"""Minimal in-memory stand-ins for the Airflow modules the operators/DAGs import.

Lets unit tests import plugins/operators and dags without Airflow installed,
and records every HttpHook call so tests can assert endpoint + headers.
No network: HttpHook.run never leaves the process.
"""

from __future__ import annotations

import importlib
import sys
import types


class FakeResponse:
    def __init__(self, payload):
        self._payload = payload

    def json(self):
        return self._payload


class Recorder:
    def __init__(self):
        self.calls = []
        self.variables = {}
        self.response = {"job_id": "job-1", "status": "completed"}


REC = Recorder()


class FakeHttpHook:
    def __init__(self, method="POST", http_conn_id=None):
        self.method = method
        self.http_conn_id = http_conn_id

    def run(self, endpoint=None, data=None, headers=None, **kw):
        REC.calls.append({
            "method": self.method,
            "conn_id": self.http_conn_id,
            "endpoint": endpoint,
            "data": data,
            "headers": dict(headers or {}),
        })
        return FakeResponse(REC.response)


class FakeVariable:
    _MISSING = object()

    @staticmethod
    def get(key, default=_MISSING, **kw):
        if key in REC.variables:
            return REC.variables[key]
        if default is FakeVariable._MISSING:
            raise KeyError(key)
        return default


class FakeTask:
    def __init__(self, task_id=None, python_callable=None, **kw):
        self.task_id = task_id
        self.python_callable = python_callable
        self.kwargs = kw
        self.downstream = []
        if FakeDAG.current is not None:
            FakeDAG.current.tasks[task_id] = self

    def __rshift__(self, other):
        self.downstream.append(other)
        return other


class FakeDAG:
    current = None

    def __init__(self, dag_id=None, **kw):
        self.dag_id = dag_id
        self.kwargs = kw
        self.tasks = {}

    def __enter__(self):
        FakeDAG.current = self
        return self

    def __exit__(self, *exc):
        FakeDAG.current = None
        return False


def install():
    """Register stub modules in sys.modules (idempotent)."""
    def mod(name, **attrs):
        m = sys.modules.get(name) or types.ModuleType(name)
        for k, v in attrs.items():
            setattr(m, k, v)
        sys.modules[name] = m
        return m

    mod("airflow")
    mod("airflow.sdk", Variable=FakeVariable, DAG=FakeDAG, Asset=lambda *a, **k: a, task=lambda f=None, **k: f)
    mod("airflow.providers")
    mod("airflow.providers.http")
    mod("airflow.providers.http.hooks")
    mod("airflow.providers.http.hooks.http", HttpHook=FakeHttpHook)
    mod("airflow.providers.http.sensors")
    mod("airflow.providers.http.sensors.http", HttpSensor=FakeTask)
    mod("airflow.providers.standard")
    mod("airflow.providers.standard.operators")
    mod("airflow.providers.standard.operators.python",
        PythonOperator=FakeTask, ShortCircuitOperator=FakeTask)
    return REC


def load_dag(path):
    """Execute a DAG file against the stubs and return its FakeDAG."""
    install()
    ns = {"__file__": path, "__name__": "dag_under_test"}
    with open(path, encoding="utf-8") as f:
        code = compile(f.read(), path, "exec")
    exec(code, ns)
    return ns["dag"]


def fresh_import(name):
    install()
    sys.modules.pop(name, None)
    return importlib.import_module(name)
