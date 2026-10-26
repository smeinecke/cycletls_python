"""Unit tests for header_values (multi-value headers) and Response.proto.

Covers the Python side of the upstream CycleTLS PR #423 / #379 ports:
Request.to_dict() must emit the "headerValues" msgpack field, and
_dict_to_response must map the Go "Proto" field onto Response.proto.
"""

from cycletls.schema import Request, _dict_to_response


class TestHeaderValuesSchema:
    def test_header_values_emitted_in_payload(self):
        req = Request(
            url="https://example.com",
            method="GET",
            header_values={"x-MULTI-value": ["one", "two"]},
        )
        payload = req.to_dict()
        assert payload["headerValues"] == {"x-MULTI-value": ["one", "two"]}

    def test_header_values_absent_when_unset(self):
        req = Request(url="https://example.com", method="GET")
        assert "headerValues" not in req.to_dict()

    def test_header_values_coexists_with_headers(self):
        req = Request(
            url="https://example.com",
            method="GET",
            headers={"accept": "text/plain"},
            header_values={"x-dup": ["a", "b"]},
        )
        payload = req.to_dict()
        assert payload["headers"] == {"accept": "text/plain"}
        assert payload["headerValues"] == {"x-dup": ["a", "b"]}


class TestResponseProto:
    def test_proto_mapped_from_go_field(self):
        resp = _dict_to_response(
            {
                "RequestID": "r1",
                "Status": 200,
                "Body": "ok",
                "Headers": {},
                "FinalUrl": "https://example.com",
                "Proto": "HTTP/2.0",
            }
        )
        assert resp.proto == "HTTP/2.0"

    def test_proto_defaults_to_none(self):
        resp = _dict_to_response(
            {
                "RequestID": "r1",
                "Status": 200,
                "Body": "ok",
                "Headers": {},
            }
        )
        assert resp.proto is None
