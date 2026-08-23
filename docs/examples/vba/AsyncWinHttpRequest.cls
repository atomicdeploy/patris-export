VERSION 1.0 CLASS
BEGIN
  MultiUse = -1  'True
END
Attribute VB_Name = "AsyncWinHttpRequest"
Attribute VB_GlobalNameSpace = False
Attribute VB_Creatable = False
Attribute VB_PredeclaredId = False
Attribute VB_Exposed = False
Option Explicit

Private Const DEFAULT_BODY_CAP_BYTES As Long = 4194304
Private Const DEFAULT_RESOLVE_TIMEOUT_MS As Long = 5000
Private Const DEFAULT_CONNECT_TIMEOUT_MS As Long = 5000
Private Const DEFAULT_SEND_TIMEOUT_MS As Long = 15000
Private Const DEFAULT_RECEIVE_TIMEOUT_MS As Long = 150000
Private Const WINHTTP_PROXY_DIRECT As Long = 1
Private Const WINHTTP_OPTION_ENABLE_REDIRECTS As Long = 6
Private Const ASYNC_ERROR_BASE As Long = 760

Private WithEvents mHttp As WinHttp.WinHttpRequest
Attribute mHttp.VB_VarHelpID = -1

Private mToken As Long
Private mRole As String
Private mStreamMode As Boolean
Private mMethod As String
Private mURL As String
Private mBodyCapBytes As Long
Private mResolveTimeoutMS As Long
Private mConnectTimeoutMS As Long
Private mSendTimeoutMS As Long
Private mReceiveTimeoutMS As Long

Private mOpened As Boolean
Private mSent As Boolean
Private mResponseStarted As Boolean
Private mResponseFinished As Boolean
Private mTerminal As Boolean
Private mFailed As Boolean
Private mAbortRequested As Boolean
Private mAborted As Boolean
Private mBodyLimitExceeded As Boolean

Private mStatusCode As Long
Private mContentType As String
Private mResponseHeaders As String
Private mResponseBody As Variant
Private mReceivedBytes As Double
Private mQueuedStreamBytes As Long
Private mStreamChunks As Collection
Private mErrorNumber As Long
Private mErrorDescription As String

Private Sub Class_Initialize()
    Set mStreamChunks = New Collection
    mBodyCapBytes = DEFAULT_BODY_CAP_BYTES
    mResolveTimeoutMS = DEFAULT_RESOLVE_TIMEOUT_MS
    mConnectTimeoutMS = DEFAULT_CONNECT_TIMEOUT_MS
    mSendTimeoutMS = DEFAULT_SEND_TIMEOUT_MS
    mReceiveTimeoutMS = DEFAULT_RECEIVE_TIMEOUT_MS
End Sub

Public Sub OpenAsync( _
        ByVal methodName As String, _
        ByVal endpoint As String, _
        ByVal requestToken As Long, _
        ByVal requestRole As String, _
        Optional ByVal streamMode As Boolean = False, _
        Optional ByVal bodyCapBytes As Long = DEFAULT_BODY_CAP_BYTES, _
        Optional ByVal resolveTimeoutMS As Long = _
            DEFAULT_RESOLVE_TIMEOUT_MS, _
        Optional ByVal connectTimeoutMS As Long = _
            DEFAULT_CONNECT_TIMEOUT_MS, _
        Optional ByVal sendTimeoutMS As Long = DEFAULT_SEND_TIMEOUT_MS, _
        Optional ByVal receiveTimeoutMS As Long = _
            DEFAULT_RECEIVE_TIMEOUT_MS)
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    If mOpened Or mSent Or mTerminal Or Not mHttp Is Nothing Then
        RecordFailure vbObjectError + ASYNC_ERROR_BASE, _
            "AsyncWinHttpRequest.OpenAsync", _
            "An asynchronous request instance can only be opened once."
        QueueOwnerDispatch
        Exit Sub
    End If

    mToken = requestToken
    mRole = Trim$(requestRole)
    mStreamMode = streamMode
    mMethod = UCase$(Trim$(methodName))
    mURL = Trim$(endpoint)
    mBodyCapBytes = bodyCapBytes
    mResolveTimeoutMS = resolveTimeoutMS
    mConnectTimeoutMS = connectTimeoutMS
    mSendTimeoutMS = sendTimeoutMS
    mReceiveTimeoutMS = receiveTimeoutMS

    If Len(mMethod) = 0 Or Len(mURL) = 0 Or Len(mRole) = 0 Then
        RecordFailure vbObjectError + ASYNC_ERROR_BASE + 1, _
            "AsyncWinHttpRequest.OpenAsync", _
            "The asynchronous request identity is incomplete."
        QueueOwnerDispatch
        Exit Sub
    End If
    If mBodyCapBytes < 1 Or _
       Not TimeoutValueIsValid(mResolveTimeoutMS) Or _
       Not TimeoutValueIsValid(mConnectTimeoutMS) Or _
       Not TimeoutValueIsValid(mSendTimeoutMS) Or _
       Not TimeoutValueIsValid(mReceiveTimeoutMS) Then
        RecordFailure vbObjectError + ASYNC_ERROR_BASE + 2, _
            "AsyncWinHttpRequest.OpenAsync", _
            "The asynchronous request limits are invalid."
        QueueOwnerDispatch
        Exit Sub
    End If

    On Error GoTo OpenFailed
    Set mHttp = New WinHttp.WinHttpRequest
    mHttp.SetTimeouts mResolveTimeoutMS, mConnectTimeoutMS, _
        mSendTimeoutMS, mReceiveTimeoutMS
    mHttp.SetProxy WINHTTP_PROXY_DIRECT
    mHttp.Open mMethod, mURL, True
    mHttp.Option(WINHTTP_OPTION_ENABLE_REDIRECTS) = False
    mOpened = True
    Exit Sub

OpenFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    RecordFailure savedErrorNumber, "AsyncWinHttpRequest.OpenAsync", _
        savedErrorDescription
    QueueOwnerDispatch
End Sub

Public Sub SetRequestHeader(ByVal headerName As String, _
                            ByVal headerValue As String)
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    If mTerminal Then Exit Sub
    If Not mOpened Or mSent Or Len(Trim$(headerName)) = 0 Or _
       HeaderHasLineBreak(headerName) Or HeaderHasLineBreak(headerValue) Then
        RecordFailure vbObjectError + ASYNC_ERROR_BASE + 3, _
            "AsyncWinHttpRequest.SetRequestHeader", _
            "The asynchronous request header is invalid."
        QueueOwnerDispatch
        Exit Sub
    End If

    On Error GoTo HeaderFailed
    mHttp.SetRequestHeader headerName, headerValue
    Exit Sub

HeaderFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    RecordFailure savedErrorNumber, _
        "AsyncWinHttpRequest.SetRequestHeader", savedErrorDescription
    QueueOwnerDispatch
End Sub

Public Sub Send(Optional ByVal requestBody As Variant)
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    If mTerminal Then Exit Sub
    If Not mOpened Or mSent Then
        RecordFailure vbObjectError + ASYNC_ERROR_BASE + 4, _
            "AsyncWinHttpRequest.Send", _
            "The asynchronous request is not ready to send."
        QueueOwnerDispatch
        Exit Sub
    End If

    On Error GoTo SendFailed
    mSent = True
    If IsMissing(requestBody) Then
        mHttp.Send
    Else
        mHttp.Send requestBody
    End If
    Exit Sub

SendFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    RecordFailure savedErrorNumber, "AsyncWinHttpRequest.Send", _
        savedErrorDescription
    QueueOwnerDispatch
End Sub

Public Sub Abort()
    If mTerminal Then Exit Sub
    mAbortRequested = True
    mAborted = True
    mTerminal = True
    AbortHttpNoThrow
    QueueOwnerDispatch
End Sub

Public Function TakeResponseBody() As Variant
    TakeResponseBody = mResponseBody
    mResponseBody = Empty
End Function

Public Function TakeStreamChunks() As Collection
    Dim chunks As Collection

    Set chunks = mStreamChunks
    Set mStreamChunks = New Collection
    mQueuedStreamBytes = 0
    Set TakeStreamChunks = chunks
End Function

Public Property Get Token() As Long
    Token = mToken
End Property

Public Property Get Role() As String
    Role = mRole
End Property

Public Property Get StreamMode() As Boolean
    StreamMode = mStreamMode
End Property

Public Property Get RequestMethod() As String
    RequestMethod = mMethod
End Property

Public Property Get RequestURL() As String
    RequestURL = mURL
End Property

Public Property Get BodyCapBytes() As Long
    BodyCapBytes = mBodyCapBytes
End Property

Public Property Get ResolveTimeoutMS() As Long
    ResolveTimeoutMS = mResolveTimeoutMS
End Property

Public Property Get ConnectTimeoutMS() As Long
    ConnectTimeoutMS = mConnectTimeoutMS
End Property

Public Property Get SendTimeoutMS() As Long
    SendTimeoutMS = mSendTimeoutMS
End Property

Public Property Get ReceiveTimeoutMS() As Long
    ReceiveTimeoutMS = mReceiveTimeoutMS
End Property

Public Property Get Opened() As Boolean
    Opened = mOpened
End Property

Public Property Get Sent() As Boolean
    Sent = mSent
End Property

Public Property Get ResponseStarted() As Boolean
    ResponseStarted = mResponseStarted
End Property

Public Property Get ResponseFinished() As Boolean
    ResponseFinished = mResponseFinished
End Property

Public Property Get Terminal() As Boolean
    Terminal = mTerminal
End Property

Public Property Get HasError() As Boolean
    HasError = mFailed
End Property

Public Property Get AbortRequested() As Boolean
    AbortRequested = mAbortRequested
End Property

Public Property Get Aborted() As Boolean
    Aborted = mAborted
End Property

Public Property Get BodyLimitExceeded() As Boolean
    BodyLimitExceeded = mBodyLimitExceeded
End Property

Public Property Get StatusCode() As Long
    StatusCode = mStatusCode
End Property

Public Property Get ContentType() As String
    ContentType = mContentType
End Property

Public Property Get ResponseHeaders() As String
    ResponseHeaders = mResponseHeaders
End Property

Public Property Get ReceivedBytes() As Double
    ReceivedBytes = mReceivedBytes
End Property

Public Property Get QueuedStreamBytes() As Long
    QueuedStreamBytes = mQueuedStreamBytes
End Property

Public Property Get QueuedStreamChunkCount() As Long
    If mStreamChunks Is Nothing Then Exit Property
    QueuedStreamChunkCount = mStreamChunks.Count
End Property

Public Property Get ErrorNumber() As Long
    ErrorNumber = mErrorNumber
End Property

Public Property Get ErrorDescription() As String
    ErrorDescription = mErrorDescription
End Property

Private Sub mHttp_OnResponseStart(ByVal Status As Long, _
                                  ByVal ContentType As String)
    Dim declaredLength As Double
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo EventFailed
    If mTerminal Then Exit Sub
    mResponseStarted = True
    mStatusCode = Status
    mContentType = ContentType
    CaptureResponseHeaders
    If DeclaredContentLength(declaredLength) Then
        If declaredLength > CDbl(mBodyCapBytes) Then
            RecordBodyLimitFailure
            AbortHttpNoThrow
        End If
    End If
    QueueOwnerDispatch
    Exit Sub

EventFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    RecordFailure savedErrorNumber, _
        "AsyncWinHttpRequest.OnResponseStart", savedErrorDescription
    AbortHttpNoThrow
    QueueOwnerDispatch
End Sub

Private Sub mHttp_OnResponseDataAvailable(Data() As Byte)
    Dim chunk As Variant
    Dim chunkLength As Long
    Dim nextByteCount As Double
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo EventFailed
    If mTerminal Then Exit Sub
    chunkLength = ByteArrayLength(Data)
    If chunkLength < 1 Then Exit Sub

    If mStreamMode Then
        nextByteCount = CDbl(mQueuedStreamBytes) + CDbl(chunkLength)
        If nextByteCount > CDbl(mBodyCapBytes) Then
            RecordBodyLimitFailure
            AbortHttpNoThrow
            QueueOwnerDispatch
            Exit Sub
        End If
        chunk = Data
        mStreamChunks.Add chunk
        mQueuedStreamBytes = CLng(nextByteCount)
        mReceivedBytes = mReceivedBytes + CDbl(chunkLength)
        QueueOwnerDispatch
        Exit Sub
    End If

    mReceivedBytes = mReceivedBytes + CDbl(chunkLength)
    If mReceivedBytes > CDbl(mBodyCapBytes) Then
        RecordBodyLimitFailure
        AbortHttpNoThrow
        QueueOwnerDispatch
        Exit Sub
    End If
    QueueOwnerDispatch
    Exit Sub

EventFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    RecordFailure savedErrorNumber, _
        "AsyncWinHttpRequest.OnResponseDataAvailable", _
        savedErrorDescription
    AbortHttpNoThrow
    QueueOwnerDispatch
End Sub

Private Sub mHttp_OnResponseFinished()
    Dim bodyValue As Variant
    Dim bodyBytes As Long
    Dim savedErrorNumber As Long
    Dim savedErrorDescription As String

    On Error GoTo EventFailed
    If mTerminal Then Exit Sub
    CaptureFinalResponseMetadata
    mResponseFinished = True

    If Not mStreamMode Then
        bodyValue = mHttp.ResponseBody
        bodyBytes = VariantByteArrayLength(bodyValue)
        mReceivedBytes = CDbl(bodyBytes)
        If mReceivedBytes > CDbl(mBodyCapBytes) Then
            RecordBodyLimitFailure
            mResponseBody = Empty
            mTerminal = True
            QueueOwnerDispatch
            Exit Sub
        End If
        mResponseBody = bodyValue
    End If

    mTerminal = True
    QueueOwnerDispatch
    Exit Sub

EventFailed:
    savedErrorNumber = Err.Number
    savedErrorDescription = Err.Description
    RecordFailure savedErrorNumber, _
        "AsyncWinHttpRequest.OnResponseFinished", savedErrorDescription
    AbortHttpNoThrow
    QueueOwnerDispatch
End Sub

Private Sub mHttp_OnError(ByVal ErrorNumber As Long, _
                          ByVal ErrorDescription As String)
    On Error Resume Next
    If mTerminal Then Exit Sub
    If mAbortRequested Then
        mAborted = True
        mTerminal = True
    Else
        RecordFailure ErrorNumber, "AsyncWinHttpRequest.OnError", _
            ErrorDescription
    End If
    QueueOwnerDispatch
    On Error GoTo 0
End Sub

Private Sub CaptureFinalResponseMetadata()
    On Error Resume Next
    mStatusCode = CLng(mHttp.Status)
    If Len(mContentType) = 0 Then
        mContentType = CStr(mHttp.GetResponseHeader("Content-Type"))
    End If
    CaptureResponseHeaders
    On Error GoTo 0
End Sub

Private Sub CaptureResponseHeaders()
    On Error Resume Next
    mResponseHeaders = CStr(mHttp.GetAllResponseHeaders)
    On Error GoTo 0
End Sub

Private Function DeclaredContentLength(ByRef contentLength As Double) As Boolean
    Dim headerValue As String
    Dim parsedValue As Double

    On Error GoTo NoLength
    headerValue = Trim$(CStr(mHttp.GetResponseHeader("Content-Length")))
    If Len(headerValue) = 0 Or Not IsNumeric(headerValue) Then Exit Function
    parsedValue = CDbl(headerValue)
    If parsedValue < 0# Or parsedValue <> Fix(parsedValue) Then Exit Function
    contentLength = parsedValue
    DeclaredContentLength = True
    Exit Function

NoLength:
    Err.Clear
End Function

Private Function ByteArrayLength(ByRef value() As Byte) As Long
    On Error GoTo EmptyArray
    ByteArrayLength = UBound(value) - LBound(value) + 1
    Exit Function

EmptyArray:
    ByteArrayLength = 0
End Function

Private Function VariantByteArrayLength(ByVal value As Variant) As Long
    On Error GoTo EmptyArray
    If (VarType(value) And vbArray) = 0 Then Exit Function
    If (VarType(value) And &HFFF&) <> vbByte Then Exit Function
    VariantByteArrayLength = UBound(value) - LBound(value) + 1
    Exit Function

EmptyArray:
    VariantByteArrayLength = 0
End Function

Private Function TimeoutValueIsValid(ByVal timeoutMS As Long) As Boolean
    TimeoutValueIsValid = timeoutMS >= 0
End Function

Private Function HeaderHasLineBreak(ByVal value As String) As Boolean
    HeaderHasLineBreak = InStr(1, value, vbCr, vbBinaryCompare) > 0 Or _
        InStr(1, value, vbLf, vbBinaryCompare) > 0
End Function

Private Sub RecordBodyLimitFailure()
    mBodyLimitExceeded = True
    RecordFailure vbObjectError + ASYNC_ERROR_BASE + 5, _
        "AsyncWinHttpRequest", _
        "The response exceeds the configured byte limit."
End Sub

Private Sub RecordFailure(ByVal errorValue As Long, _
                          ByVal errorSource As String, _
                          ByVal errorText As String)
    On Error Resume Next
    mFailed = True
    mTerminal = True
    mErrorNumber = errorValue
    mErrorDescription = Trim$(errorText)
    If Len(mErrorDescription) = 0 Then
        mErrorDescription = errorSource
    ElseIf Len(errorSource) > 0 Then
        mErrorDescription = errorSource & ": " & mErrorDescription
    End If
    On Error GoTo 0
End Sub

Private Sub AbortHttpNoThrow()
    On Error Resume Next
    mAbortRequested = True
    mAborted = True
    mTerminal = True
    If Not mHttp Is Nothing Then mHttp.Abort
    On Error GoTo 0
End Sub

Private Sub QueueOwnerDispatch()
    On Error Resume Next
    ProductCatalogSync.QueueAsyncDispatch mToken
    On Error GoTo 0
End Sub
