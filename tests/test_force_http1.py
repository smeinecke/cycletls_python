"""
Tests for force_http1 functionality.
Based on CycleTLS/tests/forceHTTP1.test.ts
"""

import os

import pytest

from cycletls import CycleTLS

_TLSFP_URL = os.environ.get("TLSFP_URL", "https://tlsfingerprint.com")
_HTTPBIN_URL = os.environ.get("HTTPBIN_URL", "https://httpbin.org")


pytestmark = pytest.mark.live


@pytest.fixture
def client():
    """Create a CycleTLS client instance.

    Connection reuse is disabled ONLY for requests against the local
    tlsfingerprint.com server (which closes the TLS connection after each
    response). Requests against httpbin.org rely on HTTP/1.1 keep-alive
    and break when reuse is force-disabled (httpbin closes idle conns
    aggressively, causing "server closed idle connection" / EOF errors
    on the next request).
    """
    cycle = CycleTLS()
    _orig = cycle.request
    def _no_reuse_for_tlsfp(method, url, **kwargs):
        if _TLSFP_URL in url:
            kwargs.setdefault("enable_connection_reuse", False)
        return _orig(method, url, **kwargs)
    cycle.request = _no_reuse_for_tlsfp
    yield cycle
    cycle.close()


@pytest.fixture
def chrome_ja3():
    """Chrome 83 JA3 fingerprint"""
    return "771,4865-4866-4867-49195-49199-49196-49200-52393-52392-49171-49172-156-157-47-53,0-23-65281-10-11-35-16-5-13-18-51-45-43-27-21,29-23-24,0"


@pytest.fixture
def chrome_user_agent():
    """Chrome 83 User Agent"""
    return "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/83.0.4103.106 Safari/537.36"


def test_http2_by_default(client, chrome_ja3, chrome_user_agent):
    """Test that HTTP/2 is used by default when server supports it"""
    url = f"{_TLSFP_URL}/api/all"


    result = client.get(
        url,
        ja3=chrome_ja3,
        user_agent=chrome_user_agent,
        force_http1=False
    )

    assert result.status_code == 200
    data = result.json()

    # Check that HTTP/2 was used
    assert "http_version" in data
    assert data["http_version"] == "h2" or data["http_version"] == "HTTP/2.0"


def test_force_http1_on_http2_server(client, chrome_ja3, chrome_user_agent):
    """Test that HTTP/1.1 is forced when force_http1 is True"""
    url = f"{_TLSFP_URL}/api/all"


    result = client.get(
        url,
        ja3=chrome_ja3,
        user_agent=chrome_user_agent,
        force_http1=True
    )

    assert result.status_code == 200
    data = result.json()

    # Check that HTTP/1.1 was used
    assert "http_version" in data
    assert data["http_version"] == "HTTP/1.1"


def test_http1_with_httpbin(client):
    """Test force_http1 with httpbin"""
    url = f"{_HTTPBIN_URL}/get"

    # First verify default behavior
    result_default = client.get(url, force_http1=False)
    assert result_default.status_code == 200

    # Then force HTTP/1.1
    result_http1 = client.get(url, force_http1=True)
    assert result_http1.status_code == 200

    # Both requests should succeed
    assert result_default.json() is not None
    assert result_http1.json() is not None


def test_http1_with_post_request(client):
    """Test that force_http1 works with POST requests"""
    url = f"{_HTTPBIN_URL}/post"

    result = client.post(
        url,
        json_data={"test": "data"},
        force_http1=True
    )

    assert result.status_code == 200
    data = result.json()
    assert "json" in data
    assert data["json"]["test"] == "data"


def test_http1_with_headers(client):
    """Test that custom headers work correctly with force_http1"""
    url = f"{_HTTPBIN_URL}/headers"

    custom_headers = {
        "X-Custom-Header": "test-value",
        "X-Another-Header": "another-value"
    }

    result = client.get(
        url,
        headers=custom_headers,
        force_http1=True
    )

    assert result.status_code == 200
    data = result.json()
    assert "headers" in data
    # go-httpbin returns header values as lists, original httpbin as strings
    def _header_value(data, key):
        val = data["headers"][key]
        return val[0] if isinstance(val, list) else val

    assert _header_value(data, "X-Custom-Header") == "test-value"
    assert _header_value(data, "X-Another-Header") == "another-value"


def test_http1_with_query_parameters(client):
    """Test that query parameters work correctly with force_http1"""
    url = f"{_HTTPBIN_URL}/get?param1=value1&param2=value2"

    result = client.get(url, force_http1=True)

    assert result.status_code == 200
    data = result.json()
    assert "args" in data
    # go-httpbin returns arg values as lists, original httpbin as strings
    def _arg_value(data, key):
        val = data["args"][key]
        return val[0] if isinstance(val, list) else val

    assert _arg_value(data, "param1") == "value1"
    assert _arg_value(data, "param2") == "value2"


def test_http1_with_cookies(client):
    """Test that cookies work correctly with force_http1"""
    url = f"{_HTTPBIN_URL}/cookies"

    # Set cookies using the cookies/set endpoint first
    set_url = f"{_HTTPBIN_URL}/cookies/set?test_cookie=test_value"
    client.get(set_url, force_http1=True)

    # Now check cookies
    result = client.get(url, force_http1=True)

    assert result.status_code == 200


def test_http1_with_redirects(client):
    """Test that redirects work correctly with force_http1"""
    url = f"{_HTTPBIN_URL}/redirect/2"

    result = client.get(url, force_http1=True)

    # Should follow redirects and succeed
    assert result.status_code == 200
    assert "url" in result.json()


def test_http1_no_redirect_follow(client):
    """Test that redirect following can be disabled with force_http1"""
    url = f"{_HTTPBIN_URL}/redirect/1"

    result = client.get(
        url,
        force_http1=True,
        disable_redirect=True
    )

    # Should return redirect status code
    assert result.status_code in [301, 302, 303, 307, 308]


def test_http1_with_compression(client):
    """Test that compression works correctly with force_http1"""
    url = f"{_HTTPBIN_URL}/gzip"

    result = client.get(url, force_http1=True)

    assert result.status_code == 200
    data = result.json()
    assert "gzipped" in data
    assert data["gzipped"] is True


def test_http1_performance_comparison(client):
    """Compare response times between HTTP/1.1 and HTTP/2"""
    import time

    url = f"{_HTTPBIN_URL}/get"

    # Test default (HTTP/2 if available, else HTTP/1.1)
    start_http2 = time.time()
    result_http2 = client.get(url, force_http1=False)
    time_http2 = time.time() - start_http2

    # Test HTTP/1.1
    start_http1 = time.time()
    result_http1 = client.get(url, force_http1=True)
    time_http1 = time.time() - start_http1

    # Both should succeed
    assert result_http2.status_code == 200
    assert result_http1.status_code == 200

    # Times should be reasonable (within 10 seconds)
    assert time_http2 < 10
    assert time_http1 < 10


@pytest.mark.parametrize("method,url", [
    ("GET", f"{_HTTPBIN_URL}/get"),
    ("POST", f"{_HTTPBIN_URL}/post"),
    ("PUT", f"{_HTTPBIN_URL}/put"),
    ("DELETE", f"{_HTTPBIN_URL}/delete"),
    ("PATCH", f"{_HTTPBIN_URL}/patch"),
])
def test_http1_with_various_methods(client, method, url):
    """Test that force_http1 works with various HTTP methods"""
    if method == "GET":
        result = client.get(url, force_http1=True)
    elif method == "POST":
        result = client.post(url, json_data={"test": "data"}, force_http1=True)
    elif method == "PUT":
        result = client.put(url, json_data={"test": "data"}, force_http1=True)
    elif method == "DELETE":
        result = client.delete(url, force_http1=True)
    elif method == "PATCH":
        result = client.patch(url, json_data={"test": "data"}, force_http1=True)

    assert result.status_code == 200
