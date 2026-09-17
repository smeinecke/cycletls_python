Module Reference
================

Autodoc-generated reference for the public ``cycletls`` API. See the
:doc:`api` guide for usage examples.

.. currentmodule:: cycletls


Clients
=======

.. autoclass:: CycleTLS
    :members:

    .. automethod:: __init__

.. autoclass:: AsyncCycleTLS
    :members:
    :show-inheritance:

.. autoclass:: Session
    :members:
    :show-inheritance:

    .. automethod:: __init__


Module-level functions
======================

Sync
----

These functions manage a global session automatically.

.. autofunction:: request
.. autofunction:: get
.. autofunction:: post
.. autofunction:: put
.. autofunction:: patch
.. autofunction:: delete
.. autofunction:: head
.. autofunction:: options

Async
-----

.. autofunction:: aget
.. autofunction:: apost
.. autofunction:: aput
.. autofunction:: apatch
.. autofunction:: adelete
.. autofunction:: ahead
.. autofunction:: aoptions
.. autofunction:: async_request
.. autofunction:: async_get
.. autofunction:: async_post
.. autofunction:: async_put
.. autofunction:: async_delete


Configuration
=============

.. autofunction:: set_default
.. autofunction:: get_default
.. autofunction:: reset_defaults
.. autofunction:: close_global_session


Response and data structures
============================

.. autoclass:: cycletls.schema.Response
    :members:

.. autoclass:: cycletls.schema.Request
    :members:

.. autoclass:: cycletls.schema.Cookie
    :members:

.. autoclass:: CaseInsensitiveDict
    :members:

.. autoclass:: CookieJar
    :members:


Fingerprints
============

.. autoclass:: TLSFingerprint
    :members:

.. autoclass:: FingerprintRegistry
    :members:

.. autoclass:: BrowserFamily
    :members:
    :undoc-members:

.. autoclass:: Platform
    :members:
    :undoc-members:

Loading profiles
----------------

.. autofunction:: load_fingerprint_from_file
.. autofunction:: load_fingerprints_from_dir
.. autofunction:: load_fingerprints_from_env
.. autofunction:: load_trackme_fingerprints
.. autofunction:: create_fingerprint_template


WebSocket
=========

.. autoclass:: WebSocketConnection
    :members:

    .. automethod:: __init__

.. autoclass:: WebSocketMessage
    :members:

.. autoclass:: MessageType
    :members:
    :undoc-members:


Server-Sent Events
==================

.. autoclass:: SSEConnection
    :members:

    .. automethod:: __init__

.. autoclass:: SSEEvent
    :members:


Exceptions
==========

All exceptions inherit from :class:`CycleTLSError`.

.. autoexception:: CycleTLSError
.. autoexception:: RequestException
.. autoexception:: HTTPError
    :show-inheritance:
.. autoexception:: ConnectionError
    :show-inheritance:
.. autoexception:: Timeout
    :show-inheritance:
.. autoexception:: ConnectTimeout
    :show-inheritance:
.. autoexception:: ReadTimeout
    :show-inheritance:
.. autoexception:: TooManyRedirects
    :show-inheritance:
.. autoexception:: InvalidURL
    :show-inheritance:
.. autoexception:: TLSError
    :show-inheritance:
.. autoexception:: ProxyError
    :show-inheritance:
.. autoexception:: InvalidHeader
    :show-inheritance:
.. autoexception:: WebSocketError
    :show-inheritance:
.. autoexception:: SSEError
    :show-inheritance:
