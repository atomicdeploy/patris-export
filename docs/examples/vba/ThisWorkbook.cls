Option Explicit

Private Sub Workbook_Open()
    On Error Resume Next
    Application.CalculateFull
    ThisWorkbook.Worksheets("Dashboard").Activate
    On Error GoTo 0
End Sub
