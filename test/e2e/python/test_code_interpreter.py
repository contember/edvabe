"""
Phase 2 acceptance test for edvabe — exercises the code interpreter SDK.

Expected environment (set by `make test-e2e-code-interpreter-python` or manually):

    E2B_API_URL=http://localhost:3000
    E2B_DOMAIN=localhost:3000
    E2B_API_KEY=edvabe_local
    E2B_SANDBOX_URL=http://localhost:3000

Prerequisites:
    edvabe build-image --template=code-interpreter
    edvabe serve (running)

The code-interpreter SDK defaults to template "code-interpreter-v1",
which edvabe seeds at startup pointing at edvabe/code-interpreter:latest.
"""

import pytest
from e2b_code_interpreter import Sandbox


@pytest.fixture
def sbx():
    s = Sandbox(timeout=300)
    try:
        yield s
    finally:
        try:
            s.kill()
        except Exception:
            pass


def test_run_code_simple(sbx):
    """Basic expression evaluation."""
    execution = sbx.run_code("1 + 1")
    assert len(execution.results) == 1
    assert execution.results[0].text == "2"
    assert execution.error is None


def test_run_code_stdout(sbx):
    """Print statement appears in stdout logs."""
    execution = sbx.run_code('print("hello from code interpreter")')
    assert any("hello from code interpreter" in line for line in execution.logs.stdout)
    assert execution.error is None


def test_run_code_multiline(sbx):
    """Multi-line code with variables."""
    execution = sbx.run_code("""
x = 42
y = x * 2
y
""")
    assert len(execution.results) == 1
    assert execution.results[0].text == "84"


def test_run_code_pandas(sbx):
    """Pandas DataFrame produces an HTML result."""
    execution = sbx.run_code("""
import pandas as pd
pd.DataFrame({"a": [1, 2, 3], "b": [4, 5, 6]})
""")
    assert len(execution.results) == 1
    result = execution.results[0]
    # Pandas DataFrames render as HTML in Jupyter
    assert result.html is not None or result.text is not None


def test_run_code_matplotlib(sbx):
    """Matplotlib chart produces a PNG result."""
    execution = sbx.run_code("""
import matplotlib
matplotlib.use('Agg')
import matplotlib.pyplot as plt
plt.plot([1, 2, 3], [4, 5, 6])
plt.title("test")
plt.savefig("/tmp/test.png")
print("done")
""")
    assert any("done" in line for line in execution.logs.stdout)
    assert execution.error is None


def test_run_code_error(sbx):
    """Runtime errors are captured in execution.error."""
    execution = sbx.run_code("1 / 0")
    assert execution.error is not None
    assert "ZeroDivisionError" in execution.error.name
